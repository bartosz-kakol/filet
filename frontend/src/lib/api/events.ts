// The single app-wide SSE connection. EventSource reconnects by itself; after
// a reconnect the registered handlers refetch state (no replay exists).

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Handler = (data: any) => void;

const TYPES = [
	'hello',
	'fs.changed',
	'upload.registered',
	'upload.progress',
	'upload.completed',
	'upload.removed',
	'job.updated',
	'job.removed',
	'smart.updated'
];

class Events {
	#es: EventSource | null = null;
	#handlers = new Map<string, Set<Handler>>();
	#reconnect = new Set<() => void>();
	#open = new Set<() => void>();
	#everOpened = false;
	#retry: ReturnType<typeof setTimeout> | null = null;
	#authCheck: (() => Promise<boolean>) | null = null;

	start(authCheck: () => Promise<boolean>) {
		this.#authCheck = authCheck;
		if (this.#es) return;
		this.#connect();
	}

	stop() {
		this.#es?.close();
		this.#es = null;
		if (this.#retry) clearTimeout(this.#retry);
	}

	#connect() {
		const es = new EventSource('/api/events');
		this.#es = es;
		es.onopen = () => {
			this.#open.forEach((f) => f());
			if (this.#everOpened) this.#reconnect.forEach((f) => f());
			this.#everOpened = true;
		};
		es.onerror = () => {
			if (es.readyState !== EventSource.CLOSED) return; // browser retries
			// A non-200 answer closes the stream for good: check the session and
			// reconnect ourselves.
			this.#es = null;
			this.#retry = setTimeout(async () => {
				const ok = (await this.#authCheck?.()) ?? true;
				if (ok && !this.#es) this.#connect();
			}, 3000);
		};
		for (const t of TYPES) {
			es.addEventListener(t, (e) => {
				let data: unknown = null;
				try {
					data = JSON.parse((e as MessageEvent).data);
				} catch {
					/* ignore */
				}
				this.#handlers.get(t)?.forEach((h) => h(data));
				this.#handlers.get('*')?.forEach((h) => h({ type: t, data }));
			});
		}
	}

	on(type: string, fn: Handler): () => void {
		let set = this.#handlers.get(type);
		if (!set) this.#handlers.set(type, (set = new Set()));
		set.add(fn);
		return () => set!.delete(fn);
	}

	/**
	 * Runs on every connection, including the first: state fetched before the
	 * stream was open may have missed events.
	 */
	onOpen(fn: () => void): () => void {
		this.#open.add(fn);
		return () => this.#open.delete(fn);
	}

	onReconnect(fn: () => void): () => void {
		this.#reconnect.add(fn);
		return () => this.#reconnect.delete(fn);
	}
}

export const events = new Events();
