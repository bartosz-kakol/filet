import { chunkCount, chunkRange, hasherCount } from './constants';

/** Computes the concatenated per-chunk BLAKE3-256 hash list of a file. */
export interface Hasher {
	hash(file: Blob, onProgress: (hashedBytes: number) => void, signal: AbortSignal): Promise<Uint8Array>;
}

interface FileJob {
	file: Blob;
	count: number;
	next: number;
	done: number;
	hashed: number;
	result: Uint8Array;
	onProgress: (n: number) => void;
	resolve: (r: Uint8Array) => void;
	reject: (e: unknown) => void;
	signal: AbortSignal;
}

interface WorkerLike {
	postMessage(msg: unknown): void;
	onmessage: ((e: MessageEvent) => void) | null;
}

/**
 * A pool of H hash workers. Chunks are scheduled round-robin across the
 * files being hashed; each worker processes one chunk at a time, so peak
 * memory stays near H × 8 MiB.
 */
export class HasherPool implements Hasher {
	#workers: WorkerLike[] = [];
	#idle: WorkerLike[] = [];
	#jobs: FileJob[] = [];
	#rr = 0;
	#tasks = new Map<number, { job: FileJob; index: number; worker: WorkerLike }>();
	#nextTask = 1;

	constructor(
		private size = hasherCount(),
		private factory: () => WorkerLike = () =>
			new Worker(new URL('./hash.worker.ts', import.meta.url), { type: 'module' }) as unknown as WorkerLike
	) {}

	hash(file: Blob, onProgress: (n: number) => void, signal: AbortSignal): Promise<Uint8Array> {
		const count = chunkCount(file.size);
		if (count === 0) return Promise.resolve(new Uint8Array(0));
		return new Promise((resolve, reject) => {
			const job: FileJob = {
				file,
				count,
				next: 0,
				done: 0,
				hashed: 0,
				result: new Uint8Array(32 * count),
				onProgress,
				resolve,
				reject,
				signal
			};
			signal.addEventListener('abort', () => this.#drop(job, new DOMException('Aborted', 'AbortError')), { once: true });
			this.#jobs.push(job);
			this.#dispatch();
		});
	}

	#drop(job: FileJob, err: unknown) {
		const i = this.#jobs.indexOf(job);
		if (i >= 0) this.#jobs.splice(i, 1);
		job.reject(err);
	}

	#worker(): WorkerLike | undefined {
		if (this.#idle.length) return this.#idle.pop();
		if (this.#workers.length < this.size) {
			const w = this.factory();
			w.onmessage = (e) => this.#onMessage(w, e.data);
			this.#workers.push(w);
			return w;
		}
		return undefined;
	}

	#dispatch() {
		for (;;) {
			const pending = this.#jobs.filter((j) => j.next < j.count);
			if (!pending.length) return;
			const w = this.#worker();
			if (!w) return;
			const job = pending[this.#rr++ % pending.length];
			const index = job.next++;
			const [start, end] = chunkRange(job.file.size, index);
			const taskId = this.#nextTask++;
			this.#tasks.set(taskId, { job, index, worker: w });
			w.postMessage({ taskId, file: job.file, start, end });
		}
	}

	#onMessage(w: WorkerLike, msg: { taskId: number; digest?: Uint8Array; error?: string }) {
		const t = this.#tasks.get(msg.taskId);
		this.#tasks.delete(msg.taskId);
		this.#idle.push(w);
		if (t && this.#jobs.includes(t.job)) {
			const { job, index } = t;
			if (msg.error || !msg.digest) {
				this.#drop(job, new Error(msg.error ?? 'hash failed'));
			} else {
				job.result.set(new Uint8Array(msg.digest), index * 32);
				job.done++;
				const [s, e] = chunkRange(job.file.size, index);
				job.hashed += e - s;
				job.onProgress(job.hashed);
				if (job.done === job.count) {
					this.#jobs.splice(this.#jobs.indexOf(job), 1);
					job.resolve(job.result);
				}
			}
		}
		this.#dispatch();
	}
}
