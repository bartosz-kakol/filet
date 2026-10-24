// The client side of the resumable upload protocol:
// preflight → conflict prompts → queue (N slots) → hashing → registration →
// chunk transfer (P per file, G global, final chunk last) → done.

import { formatBytes } from '$lib/files/format';
import { parentPath } from '$lib/files/path';
import {
	CHUNK_SIZE,
	GLOBAL,
	IN_PROGRESS_RETRY_MS,
	PER_FILE,
	PREFLIGHT_BATCH,
	backoffMs,
	chunkRange,
	expandRanges,
	toB64Url
} from './constants';
import type { Hasher } from './hasher';
import type { HttpResult, PreflightResult, Transport } from './transport';

export type UploadState =
	| 'queued'
	| 'hashing'
	| 'registering'
	| 'uploading'
	| 'waiting-network'
	| 'paused-auth'
	| 'finalizing'
	| 'done'
	| 'skipped'
	| 'cancelled'
	| 'failed'
	| 'corrupted'
	| 'removed';

export const ACTIVE_STATES: ReadonlySet<UploadState> = new Set([
	'hashing',
	'registering',
	'uploading',
	'waiting-network',
	'paused-auth',
	'finalizing'
]);
export const TERMINAL_STATES: ReadonlySet<UploadState> = new Set([
	'done',
	'skipped',
	'cancelled',
	'failed',
	'corrupted',
	'removed'
]);

export interface NewUpload {
	file: File;
	place: string;
	path: string;
}

export interface ConflictRequest {
	item: UploadItem;
	status: 'conflict' | 'uploading';
	existing?: PreflightResult['existing'];
	suggestedName: string;
	/** Conflicts left in this batch including this one, when known. */
	remaining: number;
}

export interface ConflictAnswer {
	action: 'overwrite' | 'rename' | 'skip';
	applyToAll: boolean;
}

export interface UploadDeps {
	transport: Transport;
	hasher: Hasher;
	prompt: (c: ConflictRequest) => Promise<ConflictAnswer>;
	concurrency: () => number;
	onAuthRequired?: () => void;
	onCompleted?: (item: UploadItem) => void;
	placeName?: (id: string) => string;
	isOnline?: () => boolean;
}

let keySeq = 0;

export class UploadItem {
	readonly key = `u${++keySeq}`;
	readonly file: File;
	readonly place: string;
	readonly size: number;
	readonly mtime: number;

	path = $state('');
	state = $state<UploadState>('queued');
	message = $state('');
	note = $state('');
	hashedBytes = $state(0);
	hashSpeed = $state(0);
	sentBytes = $state(0);
	speed = $state(0);
	resumedPct = $state<number | null>(null);
	savedAs = $state<string | null>(null);
	preflighting = $state(true);

	conflict: 'fail' | 'overwrite' = 'fail';
	expectResume = false;
	ready = false;
	serverId: string | null = null;
	hashes: Uint8Array | null = null;
	chunks = 0;
	pending: number[] = [];
	finalIndex = -1;
	finalState: 'held' | 'sent' = 'held';
	acked = new Set<number>();
	inflight = new Map<number, AbortController>();
	loaded = new Map<number, number>();
	transientStreak = 0;
	retrying = 0;
	pausedFrom: UploadState | null = null;
	hashAbort: AbortController | null = null;
	finish: (() => void) | null = null;
	onSettled: ((item: UploadItem) => void) | null = null;
	lastSample = { at: 0, sent: 0, hashed: 0 };

	constructor(n: NewUpload) {
		this.file = n.file;
		this.place = n.place;
		this.path = n.path;
		this.size = n.file.size;
		this.mtime = n.file.lastModified;
	}

	get name() {
		return this.path.slice(this.path.lastIndexOf('/') + 1);
	}
	get dir() {
		return parentPath(this.path);
	}
	get active() {
		return ACTIVE_STATES.has(this.state);
	}
	get terminal() {
		return TERMINAL_STATES.has(this.state);
	}
	get hashProgress() {
		return this.size ? this.hashedBytes / this.size : this.hashes ? 1 : 0;
	}
	get progress() {
		if (this.state === 'done') return 1;
		return this.size ? Math.min(1, this.sentBytes / this.size) : 0;
	}

	chunkLen(i: number) {
		const [s, e] = chunkRange(this.size, i);
		return e - s;
	}

	recomputeSent() {
		let n = 0;
		for (const i of this.acked) n += this.chunkLen(i);
		for (const l of this.loaded.values()) n += l;
		this.sentBytes = Math.min(n, this.size);
	}
}

const TRANSIENT = new Set([0, 500, 502, 503, 504]);

function errCode(r: HttpResult): string {
	return r.body?.error?.code ?? '';
}

function errMessage(r: HttpResult, fallback: string): string {
	return r.body?.error?.message ?? fallback;
}

const delay = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

export class UploadManager {
	items = $state<UploadItem[]>([]);
	authPaused = $state(false);

	#inflight = 0;
	#rr = 0;
	#authWaiters: (() => void)[] = [];
	#retryTimers = new Map<ReturnType<typeof setTimeout>, () => void>();
	#sampler: ReturnType<typeof setInterval> | null = null;

	constructor(private deps: UploadDeps) {}

	/** Number of chunk requests in flight across all files (tests). */
	get inflightCount() {
		return this.#inflight;
	}

	get activeCount() {
		return this.items.filter((i) => i.active).length;
	}

	get hasActive() {
		return this.items.some((i) => i.active || (i.state === 'queued' && !i.terminal));
	}

	/**
	 * Adds a batch: creates folders (parents), preflights, asks about
	 * conflicts (with "apply to all" across the batch) and queues the rest.
	 */
	async add(
		uploads: NewUpload[],
		opts: {
			dirs?: { place: string; path: string }[];
			expectResume?: boolean;
			onSettled?: (item: UploadItem) => void;
		} = {}
	): Promise<UploadItem[]> {
		const items = uploads.map((u) => {
			const it = new UploadItem(u);
			it.expectResume = !!opts.expectResume;
			it.onSettled = opts.onSettled ?? null;
			return it;
		});
		this.items.push(...items);
		for (const d of opts.dirs ?? []) {
			await this.#withRetry(() => this.deps.transport.mkdir(d.place, d.path));
		}
		await this.#preflightAndQueue(items);
		return items;
	}

	/** Restarts a failed/removed/corrupted item from preflight. */
	async retry(item: UploadItem) {
		if (!item.terminal || item.state === 'done') return;
		Object.assign(item, {
			conflict: 'fail',
			ready: false,
			serverId: null,
			hashes: null,
			chunks: 0,
			pending: [],
			finalIndex: -1,
			finalState: 'held',
			acked: new Set(),
			inflight: new Map(),
			loaded: new Map(),
			transientStreak: 0,
			retrying: 0
		});
		item.state = 'queued';
		item.message = '';
		item.note = '';
		item.hashedBytes = 0;
		item.sentBytes = 0;
		item.resumedPct = null;
		item.preflighting = true;
		await this.#preflightAndQueue([item]);
	}

	cancel(item: UploadItem) {
		if (item.terminal) return;
		item.hashAbort?.abort();
		this.#abortChunks(item);
		const id = item.serverId;
		this.#settle(item, 'cancelled');
		if (id) void this.deps.transport.cancel(id);
	}

	clearFinished() {
		this.items = this.items.filter((i) => !i.terminal);
	}

	/** Called after a successful re-login. */
	resumeAuth() {
		if (!this.authPaused) return;
		this.authPaused = false;
		for (const it of this.items) {
			if (it.state === 'paused-auth') {
				it.state = it.pausedFrom ?? 'uploading';
				it.pausedFrom = null;
			}
		}
		const w = this.#authWaiters;
		this.#authWaiters = [];
		w.forEach((f) => f());
		this.#tick();
	}

	/** The browser came back online: retry waiting chunks immediately. */
	online() {
		const timers = [...this.#retryTimers];
		this.#retryTimers.clear();
		for (const [t, fn] of timers) {
			clearTimeout(t);
			fn();
		}
		this.#tick();
	}

	// --- preflight & conflicts ---

	async #preflightAndQueue(items: UploadItem[]) {
		const results = new Map<string, PreflightResult>();
		for (let i = 0; i < items.length; i += PREFLIGHT_BATCH) {
			const batch = items.slice(i, i + PREFLIGHT_BATCH);
			const r = await this.#withRetry(() =>
				this.deps.transport.preflight(batch.map((it) => ({ key: it.key, place: it.place, path: it.path, size: it.size })))
			);
			if (r.status !== 200) {
				for (const it of batch) this.#fail(it, errMessage(r, 'Upload check failed'));
				continue;
			}
			for (const res of r.body.items as PreflightResult[]) results.set(res.key, res);
		}
		const conflicts: { item: UploadItem; res: PreflightResult }[] = [];
		for (const it of items) {
			if (it.terminal) continue;
			it.preflighting = false;
			const res = results.get(it.key);
			if (!res) continue;
			switch (res.status) {
				case 'ok':
					it.ready = true;
					break;
				case 'resumable':
					it.ready = true;
					it.note = `Will resume (${it.size ? Math.round(((res.receivedBytes ?? 0) / it.size) * 100) : 0} % on server)`;
					it.expectResume = true;
					break;
				case 'conflict':
				case 'uploading':
					conflicts.push({ item: it, res });
					break;
				default:
					this.#fail(it, this.#preflightMessage(it, res));
			}
		}
		this.#pump(); // hashing of non-conflicting items starts while prompting
		let applyAll: ConflictAnswer['action'] | null = null;
		for (let i = 0; i < conflicts.length; i++) {
			const { item, res } = conflicts[i];
			if (item.terminal) continue;
			let action = applyAll;
			if (!action) {
				const ans = await this.deps.prompt({
					item,
					status: res.status as 'conflict' | 'uploading',
					existing: res.existing,
					suggestedName: res.suggestedName ?? item.name,
					remaining: conflicts.length - i
				});
				action = ans.action;
				if (ans.applyToAll) applyAll = ans.action;
			}
			this.#applyConflict(item, action, res.suggestedName ?? item.name);
			this.#pump();
		}
	}

	#preflightMessage(it: UploadItem, res: PreflightResult): string {
		switch (res.status) {
			case 'readonly':
				return 'Place is read-only';
			case 'insufficient_space': {
				const place = this.deps.placeName?.(it.place) ?? it.place;
				return `Not enough space on ${place}: needs ${formatBytes(res.required ?? it.size)}, ${formatBytes(res.available ?? 0)} free`;
			}
			case 'reserved_name':
				return 'This name is reserved by Filet';
			case 'invalid_path':
				return 'Invalid file name or path';
			case 'parent_not_directory':
				return 'A folder in the path is a file';
			case 'permission_denied':
				return 'Permission denied';
			case 'place_not_found':
				return 'Place no longer exists';
		}
		return 'Cannot upload';
	}

	#applyConflict(item: UploadItem, action: ConflictAnswer['action'], suggested: string) {
		switch (action) {
			case 'overwrite':
				item.conflict = 'overwrite';
				item.ready = true;
				break;
			case 'rename':
				item.path = item.dir === '/' ? '/' + suggested : item.dir + '/' + suggested;
				item.conflict = 'fail';
				item.ready = true;
				break;
			case 'skip':
				this.#settle(item, 'skipped');
		}
	}

	// --- queue ---

	#pump() {
		let active = this.items.filter((i) => i.active).length;
		const n = this.deps.concurrency();
		for (const it of this.items) {
			if (active >= n) break;
			if (it.state === 'queued' && it.ready) {
				active++;
				void this.#run(it);
			}
		}
	}

	/** Re-evaluates the slots (called when the concurrency changes). */
	concurrencyChanged() {
		this.#pump();
	}

	async #run(item: UploadItem) {
		item.state = 'hashing';
		this.#startSampler();
		try {
			if (!item.hashes) {
				item.hashAbort = new AbortController();
				item.hashedBytes = 0;
				item.hashes = await this.deps.hasher.hash(item.file, (n) => (item.hashedBytes = n), item.hashAbort.signal);
				item.hashedBytes = item.size;
			}
			if (item.terminal) return;
			const proceed = await this.#register(item);
			if (!proceed || item.terminal) return;
			item.state = 'uploading';
			await new Promise<void>((res) => {
				item.finish = res;
				this.#tick();
			});
		} catch (e) {
			if (!item.terminal) this.#fail(item, e instanceof Error ? e.message : String(e));
		} finally {
			this.#pump();
		}
	}

	async #whenAuthed() {
		while (this.authPaused) await new Promise<void>((r) => this.#authWaiters.push(r));
	}

	#authFail(item?: UploadItem) {
		if (item && !item.terminal && item.state !== 'paused-auth') {
			item.pausedFrom = item.state;
			item.state = 'paused-auth';
		}
		if (this.authPaused) return;
		this.authPaused = true;
		for (const it of this.items) {
			if (it.active && it.state !== 'paused-auth') {
				it.pausedFrom = it.state;
				it.state = 'paused-auth';
			}
		}
		this.deps.onAuthRequired?.();
	}

	/** Retries fn on transient failures and 401 until it gets an answer. */
	async #withRetry(fn: () => Promise<HttpResult>): Promise<HttpResult> {
		let streak = 0;
		for (;;) {
			await this.#whenAuthed();
			const r = await fn();
			if (r.status === 401) {
				this.#authFail();
				continue;
			}
			if (!TRANSIENT.has(r.status)) return r;
			streak++;
			await delay(backoffMs(streak));
		}
	}

	/** Registers the upload; returns false when the item settled instead. */
	async #register(item: UploadItem): Promise<boolean> {
		for (;;) {
			await this.#whenAuthed();
			if (item.terminal) return false;
			item.state = 'registering';
			const r = await this.deps.transport.register({
				place: item.place,
				path: item.path,
				size: item.size,
				mtime: item.mtime,
				chunkSize: CHUNK_SIZE,
				hashes: toB64Url(item.hashes ?? new Uint8Array(0)),
				conflict: item.conflict
			});
			if (item.terminal) return false;
			if (r.status === 401) {
				this.#authFail(item);
				continue;
			}
			if (TRANSIENT.has(r.status)) {
				item.transientStreak++;
				if (item.transientStreak >= 3 || !this.#isOnline()) item.state = 'waiting-network';
				await this.#retryDelay(backoffMs(item.transientStreak));
				continue;
			}
			item.transientStreak = 0;
			if (r.status === 200 || r.status === 201) {
				const body = r.body;
				if (body.status === 'complete') {
					if (body.entry?.name && body.entry.name !== item.name) item.savedAs = body.entry.name;
					this.#settle(item, 'done');
					this.deps.onCompleted?.(item);
					return false;
				}
				this.#prepareChunks(item, body);
				return true;
			}
			const code = errCode(r);
			if (r.status === 409 && (code === 'already_exists' || code === 'upload_conflict')) {
				const suggested = (r.body?.error?.details?.suggestedName as string) ?? item.name;
				const ans = await this.deps.prompt({
					item,
					status: code === 'upload_conflict' ? 'uploading' : 'conflict',
					suggestedName: suggested,
					remaining: 1
				});
				this.#applyConflict(item, ans.action, suggested);
				if (item.terminal) return false;
				continue;
			}
			if (r.status === 507) {
				const d = r.body?.error?.details ?? {};
				const place = this.deps.placeName?.(item.place) ?? item.place;
				this.#fail(item, `Not enough space on ${place}: needs ${formatBytes(d.required)}, ${formatBytes(d.available)} free`);
				return false;
			}
			this.#fail(item, errMessage(r, `Upload failed (${r.status})`));
			return false;
		}
	}

	#prepareChunks(item: UploadItem, body: { id: string; status: string; chunkCount: number; missing: [number, number][] }) {
		item.serverId = body.id;
		item.chunks = body.chunkCount;
		const missing = expandRanges(body.missing ?? []);
		const missingSet = new Set(missing);
		item.acked = new Set();
		for (let i = 0; i < item.chunks; i++) if (!missingSet.has(i)) item.acked.add(i);
		item.loaded = new Map();
		item.recomputeSent();
		if (body.status === 'resumed' && item.sentBytes > 0) {
			item.resumedPct = Math.round((item.sentBytes / item.size) * 100);
			item.note = `Resumed — ${item.resumedPct} % was already on the server`;
		} else if (item.note.startsWith('Will resume')) {
			item.note = '';
		}
		if (item.expectResume && body.status === 'new') {
			item.note = 'Content differed from the interrupted upload; uploading from the beginning';
		}
		if (missing.length === 0) {
			// Everything reached the server before an interruption: re-send the
			// last chunk with the final flag (idempotent).
			item.finalIndex = item.chunks - 1;
			item.pending = [];
		} else {
			item.finalIndex = missing[missing.length - 1];
			item.pending = missing.slice(0, -1);
		}
		item.finalState = 'held';
	}

	// --- chunk scheduling ---

	#canSend(it: UploadItem): boolean {
		if (it.state !== 'uploading' && it.state !== 'waiting-network') return false;
		if (it.inflight.size >= PER_FILE) return false;
		if (it.pending.length > 0) return true;
		return it.finalState === 'held' && it.inflight.size === 0 && it.retrying === 0 && it.finalIndex >= 0;
	}

	#tick() {
		if (this.authPaused) return;
		while (this.#inflight < GLOBAL) {
			const candidates = this.items.filter((i) => this.#canSend(i));
			if (!candidates.length) return;
			const it = candidates[this.#rr++ % candidates.length];
			if (it.pending.length > 0) {
				void this.#send(it, it.pending.shift()!, false);
			} else {
				it.finalState = 'sent';
				void this.#send(it, it.finalIndex, true);
			}
		}
	}

	#requeue(it: UploadItem, idx: number, final: boolean) {
		if (final) {
			it.finalState = 'held';
		} else if (!it.pending.includes(idx)) {
			it.pending.push(idx);
			it.pending.sort((a, b) => a - b);
		}
	}

	#retryLater(it: UploadItem, ms: number, idx: number, final: boolean) {
		it.retrying++;
		const fn = () => {
			it.retrying--;
			if (!it.terminal) this.#requeue(it, idx, final);
			this.#tick();
		};
		const t = setTimeout(() => {
			this.#retryTimers.delete(t);
			fn();
		}, ms);
		this.#retryTimers.set(t, fn);
	}

	#retryDelay(ms: number): Promise<void> {
		return new Promise((res) => {
			const t = setTimeout(() => {
				this.#retryTimers.delete(t);
				res();
			}, ms);
			this.#retryTimers.set(t, res);
		});
	}

	#isOnline() {
		return this.deps.isOnline ? this.deps.isOnline() : typeof navigator === 'undefined' || navigator.onLine !== false;
	}

	async #send(it: UploadItem, idx: number, final: boolean) {
		const ac = new AbortController();
		it.inflight.set(idx, ac);
		this.#inflight++;
		const [start, end] = chunkRange(it.size, idx);
		let r: HttpResult;
		try {
			r = await this.deps.transport.putChunk(
				it.serverId!,
				idx,
				it.file.slice(start, end),
				final,
				(loaded) => {
					it.loaded.set(idx, loaded);
					it.recomputeSent();
				},
				ac.signal
			);
		} catch {
			r = { status: 0, body: null };
		}
		this.#inflight--;
		it.inflight.delete(idx);
		it.loaded.delete(idx);
		if (ac.signal.aborted || it.terminal) {
			it.recomputeSent();
			this.#tick();
			return;
		}
		this.#handleChunk(it, idx, final, r);
		it.recomputeSent();
		this.#tick();
	}

	#handleChunk(it: UploadItem, idx: number, final: boolean, r: HttpResult) {
		const code = errCode(r);
		if (r.status === 200) {
			it.transientStreak = 0;
			if (it.state === 'waiting-network') it.state = 'uploading';
			it.acked.add(idx);
			if (final) {
				if (r.body?.renamed && r.body?.entry?.name) it.savedAs = r.body.entry.name;
				this.#settle(it, 'done');
				this.deps.onCompleted?.(it);
			}
			return;
		}
		if (TRANSIENT.has(r.status)) {
			it.transientStreak++;
			if (it.transientStreak >= 3 || !this.#isOnline()) it.state = 'waiting-network';
			this.#retryLater(it, backoffMs(it.transientStreak), idx, final);
			return;
		}
		if (r.status === 401) {
			this.#requeue(it, idx, final);
			this.#authFail(it);
			return;
		}
		if (r.status === 409 && code === 'chunk_in_progress') {
			this.#retryLater(it, IN_PROGRESS_RETRY_MS, idx, final);
			return;
		}
		if (r.status === 422 && code === 'chunk_hash_mismatch') {
			// Corrupted in transit: re-read the slice and send it again now.
			this.#requeue(it, idx, final);
			return;
		}
		if (r.status === 422 && code === 'upload_corrupted') {
			this.#abortChunks(it);
			this.#settle(it, 'corrupted', 'a chunk failed verification 3 times');
			return;
		}
		if (r.status === 410) {
			this.#abortChunks(it);
			this.#settle(it, 'removed', 'Removed on the server');
			return;
		}
		if (r.status === 409 && code === 'chunks_missing') {
			const missing = expandRanges((r.body?.error?.details?.missing as [number, number][]) ?? []);
			if (final) it.acked.add(idx);
			for (const m of missing) {
				it.acked.delete(m);
				if (m !== it.finalIndex) this.#requeue(it, m, false);
			}
			it.finalState = 'held';
			return;
		}
		this.#abortChunks(it);
		if (r.status === 507) {
			const place = this.deps.placeName?.(it.place) ?? it.place;
			this.#fail(it, `Not enough space on ${place}`);
		} else {
			this.#fail(it, errMessage(r, `Upload failed (${r.status})`));
		}
	}

	#abortChunks(it: UploadItem) {
		for (const ac of it.inflight.values()) ac.abort();
		it.pending = [];
		it.finalState = 'sent';
	}

	#fail(it: UploadItem, message: string) {
		this.#settle(it, 'failed', message);
	}

	#settle(it: UploadItem, state: UploadState, message = '') {
		it.state = state;
		it.preflighting = false;
		if (message) it.message = message;
		it.speed = 0;
		it.finish?.();
		it.finish = null;
		it.onSettled?.(it);
		this.#pump();
	}

	// --- speed sampling (500 ms, EMA α = 0.3) ---

	#startSampler() {
		if (this.#sampler || typeof setInterval === 'undefined') return;
		this.#sampler = setInterval(() => this.#sample(), 500);
	}

	#sample() {
		const now = performance.now();
		let any = false;
		for (const it of this.items) {
			if (!it.active) continue;
			any = true;
			const s = it.lastSample;
			if (s.at > 0) {
				const dt = (now - s.at) / 1000;
				const up = Math.max(0, it.sentBytes - s.sent) / dt;
				const hs = Math.max(0, it.hashedBytes - s.hashed) / dt;
				it.speed = it.speed ? 0.3 * up + 0.7 * it.speed : up;
				it.hashSpeed = it.hashSpeed ? 0.3 * hs + 0.7 * it.hashSpeed : hs;
			}
			it.lastSample = { at: now, sent: it.sentBytes, hashed: it.hashedBytes };
		}
		if (!any && this.#sampler) {
			clearInterval(this.#sampler);
			this.#sampler = null;
		}
	}
}
