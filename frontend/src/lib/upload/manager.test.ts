import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { CHUNK_SIZE, GLOBAL, PER_FILE, backoffMs } from './constants';
import type { Hasher } from './hasher';
import { UploadManager, type ConflictRequest, type UploadItem } from './manager.svelte';
import type { HttpResult, PreflightItem, RegisterRequest, Transport } from './transport';

const settle = () => vi.advanceTimersByTimeAsync(0);

function file(size: number, name = 'f.bin') {
	return new File([new Uint8Array(size)], name, { lastModified: 1_700_000_000_000 });
}

interface PendingPut {
	id: string;
	index: number;
	final: boolean;
	resolve: (r: HttpResult) => void;
}

/** A controllable fake server. */
class FakeTransport implements Transport {
	preflightCalls: PreflightItem[][] = [];
	registers: RegisterRequest[] = [];
	puts: PendingPut[] = [];
	log: { id: string; index: number; final: boolean }[] = [];
	auto = true;
	preflightStatus: (it: PreflightItem) => Record<string, unknown> = () => ({ status: 'ok' });
	registerResult: (r: RegisterRequest, n: number) => HttpResult = (r, n) => ({
		status: 201,
		body: { id: `s-${r.path}-${n}`, status: 'new', chunkSize: CHUNK_SIZE, chunkCount: Math.ceil(r.size / CHUNK_SIZE), missing: [[0, Math.ceil(r.size / CHUNK_SIZE) - 1]] }
	});
	chunkResult: (p: { id: string; index: number; final: boolean }) => HttpResult = (p) =>
		p.final ? { status: 200, body: { status: 'complete', entry: { name: 'x' } } } : { status: 200, body: { receivedCount: 1 } };
	cancelled: string[] = [];

	async preflight(items: PreflightItem[]) {
		this.preflightCalls.push(items);
		return { status: 200, body: { items: items.map((it) => ({ key: it.key, ...this.preflightStatus(it) })) } };
	}
	async register(req: RegisterRequest) {
		this.registers.push(req);
		return this.registerResult(req, this.registers.length);
	}
	putChunk(id: string, index: number, _b: Blob, final: boolean, _p: (n: number) => void, signal: AbortSignal) {
		this.log.push({ id, index, final });
		if (this.auto) return Promise.resolve(this.chunkResult({ id, index, final }));
		return new Promise<HttpResult>((resolve) => {
			const p = { id, index, final, resolve };
			this.puts.push(p);
			signal.addEventListener('abort', () => resolve({ status: 0, body: { aborted: true } }));
		});
	}
	async cancel(id: string) {
		this.cancelled.push(id);
	}
	async mkdir() {
		return { status: 201, body: {} };
	}
	/** Resolves pending PUTs (all by default). */
	resolve(filter: (p: PendingPut) => boolean = () => true, res?: (p: PendingPut) => HttpResult) {
		const now = this.puts.filter(filter);
		this.puts = this.puts.filter((p) => !filter(p));
		for (const p of now) p.resolve(res ? res(p) : this.chunkResult(p));
	}
}

const zeroHasher: Hasher = {
	hash: async (f) => new Uint8Array(32 * Math.ceil(f.size / CHUNK_SIZE))
};

function setup(opts: { concurrency?: number; prompt?: (c: ConflictRequest) => Promise<{ action: 'overwrite' | 'rename' | 'skip'; applyToAll: boolean }> } = {}) {
	const t = new FakeTransport();
	let concurrency = opts.concurrency ?? 4;
	const authRequired = vi.fn();
	const hashSpy = vi.fn(zeroHasher.hash);
	const m = new UploadManager({
		transport: t,
		hasher: { hash: hashSpy },
		prompt: opts.prompt ?? (async () => ({ action: 'skip', applyToAll: false })),
		concurrency: () => concurrency,
		onAuthRequired: authRequired,
		isOnline: () => true
	});
	return { m, t, authRequired, hashSpy, setConcurrency: (n: number) => (concurrency = n) };
}

beforeEach(() => {
	vi.useFakeTimers();
});
afterEach(() => {
	vi.useRealTimers();
});

describe('upload state machine', () => {
	it('uploads all chunks and sends the final chunk last', async () => {
		const { m, t } = setup();
		t.auto = false;
		const [item] = await m.add([{ file: file(2 * CHUNK_SIZE + 5), place: 'p', path: '/a.bin' }]);
		await settle();
		expect(item.state).toBe('uploading');
		// Chunks 0 and 1 go out; the final chunk (2) is held back.
		expect(t.puts.map((p) => p.index)).toEqual([0, 1]);
		t.resolve((p) => p.index === 0);
		await settle();
		expect(t.puts.map((p) => p.index)).toEqual([1]);
		t.resolve();
		await settle();
		expect(t.puts.map((p) => [p.index, p.final])).toEqual([[2, true]]);
		t.resolve();
		await settle();
		expect(item.state).toBe('done');
		expect(item.progress).toBe(1);
	});

	it('limits chunks to P per file and G globally', async () => {
		const { m, t } = setup({ concurrency: 4 });
		t.auto = false;
		await m.add([1, 2, 3, 4].map((n) => ({ file: file(6 * CHUNK_SIZE), place: 'p', path: `/f${n}` })));
		await settle();
		let maxGlobal = 0;
		for (let round = 0; round < 40 && t.puts.length; round++) {
			maxGlobal = Math.max(maxGlobal, t.puts.length);
			const perFile = new Map<string, number>();
			for (const p of t.puts) perFile.set(p.id, (perFile.get(p.id) ?? 0) + 1);
			for (const n of perFile.values()) expect(n).toBeLessThanOrEqual(PER_FILE);
			expect(t.puts.length).toBeLessThanOrEqual(GLOBAL);
			t.resolve((p) => p === t.puts[0]);
			await settle();
		}
		expect(maxGlobal).toBe(GLOBAL);
		expect(m.items.every((i) => i.state === 'done')).toBe(true);
	});

	it('follows the file concurrency limit live', async () => {
		const { m, t, setConcurrency } = setup({ concurrency: 2 });
		t.auto = false;
		await m.add([1, 2, 3, 4, 5, 6].map((n) => ({ file: file(CHUNK_SIZE), place: 'p', path: `/f${n}` })));
		await settle();
		expect(m.items.filter((i) => i.active).length).toBe(2);
		setConcurrency(4);
		m.concurrencyChanged();
		await settle();
		expect(m.items.filter((i) => i.active).length).toBe(4);
		setConcurrency(1);
		m.concurrencyChanged();
		await settle();
		expect(m.items.filter((i) => i.active).length).toBe(4); // running files are not preempted
	});

	it('re-sends a mismatched chunk and stops after the server reports corruption', async () => {
		const { m, t } = setup();
		let attempts = 0;
		t.chunkResult = () => {
			attempts++;
			if (attempts < 3)
				return { status: 422, body: { error: { code: 'chunk_hash_mismatch', details: { attempt: attempts, maxAttempts: 3 } } } };
			return { status: 422, body: { error: { code: 'upload_corrupted', message: 'x' } } };
		};
		const [item] = await m.add([{ file: file(100), place: 'p', path: '/c' }]);
		await settle();
		expect(t.log.map((l) => l.index)).toEqual([0, 0, 0]);
		expect(item.state).toBe('corrupted');
	});

	it('retries transient failures with backoff and shows waiting-network', async () => {
		const { m, t } = setup();
		let fails = 4;
		t.chunkResult = (p) => (fails-- > 0 ? { status: 0, body: null } : p.final ? { status: 200, body: { status: 'complete' } } : { status: 200, body: {} });
		const [item] = await m.add([{ file: file(100), place: 'p', path: '/n' }]);
		await settle();
		expect(t.log.length).toBe(1);
		await vi.advanceTimersByTimeAsync(backoffMs(1));
		expect(t.log.length).toBe(2);
		await vi.advanceTimersByTimeAsync(backoffMs(2));
		expect(t.log.length).toBe(3);
		expect(item.state).toBe('waiting-network');
		await vi.advanceTimersByTimeAsync(backoffMs(3));
		expect(t.log.length).toBe(4);
		// The online event retries immediately instead of waiting 8 s.
		m.online();
		await settle();
		expect(t.log.length).toBe(5);
		expect(item.state).toBe('done');
	});

	it('re-sends the last chunk with the final flag when nothing is missing', async () => {
		const { m, t } = setup();
		t.registerResult = () => ({ status: 200, body: { id: 's', status: 'resumed', chunkCount: 3, missing: [] } });
		const [item] = await m.add([{ file: file(3 * CHUNK_SIZE), place: 'p', path: '/r' }]);
		await settle();
		expect(t.log).toEqual([{ id: 's', index: 2, final: true }]);
		expect(item.resumedPct).toBe(100);
		expect(item.state).toBe('done');
	});

	it('uploads only missing chunks on resume and handles chunks_missing', async () => {
		const { m, t } = setup();
		t.registerResult = () => ({ status: 200, body: { id: 's', status: 'resumed', chunkCount: 4, missing: [[1, 1], [3, 3]] } });
		let first = true;
		t.chunkResult = (p) => {
			if (p.final && first) {
				first = false;
				return { status: 409, body: { error: { code: 'chunks_missing', details: { missing: [[0, 0]] } } } };
			}
			return p.final ? { status: 200, body: { status: 'complete' } } : { status: 200, body: {} };
		};
		const [item] = await m.add([{ file: file(4 * CHUNK_SIZE), place: 'p', path: '/m' }]);
		await settle();
		expect(t.log.map((l) => `${l.index}${l.final ? 'F' : ''}`)).toEqual(['1', '3F', '0', '3F']);
		expect(item.resumedPct).toBe(50);
		expect(item.state).toBe('done');
	});

	it('marks removed sessions on 410', async () => {
		const { m, t } = setup();
		t.chunkResult = () => ({ status: 410, body: { error: { code: 'upload_gone' } } });
		const [item] = await m.add([{ file: file(10), place: 'p', path: '/g' }]);
		await settle();
		expect(item.state).toBe('removed');
	});

	it('pauses every upload on 401 and continues after re-login', async () => {
		const { m, t, authRequired } = setup();
		let authed = false;
		t.chunkResult = (p) => (!authed ? { status: 401, body: { error: { code: 'unauthenticated' } } } : p.final ? { status: 200, body: {} } : { status: 200, body: {} });
		const [item] = await m.add([{ file: file(10), place: 'p', path: '/a' }]);
		await settle();
		expect(item.state).toBe('paused-auth');
		expect(authRequired).toHaveBeenCalledOnce();
		authed = true;
		m.resumeAuth();
		await settle();
		expect(item.state).toBe('done');
	});

	it('applies "apply to all" to the remaining conflicts of a batch', async () => {
		const prompt = vi.fn(async () => ({ action: 'rename' as const, applyToAll: true }));
		const { m, t } = setup({ prompt });
		t.preflightStatus = (it) => (it.path.startsWith('/c') ? { status: 'conflict', suggestedName: it.path.slice(1) + ' (1)' } : { status: 'ok' });
		const items = await m.add(['/c1', '/ok', '/c2', '/c3'].map((p) => ({ file: file(1), place: 'p', path: p })));
		await settle();
		expect(prompt).toHaveBeenCalledOnce();
		expect((prompt.mock.calls[0] as unknown as [ConflictRequest])[0].remaining).toBe(3);
		expect(items.map((i) => i.path)).toEqual(['/c1 (1)', '/ok', '/c2 (1)', '/c3 (1)']);
	});

	it('fails items without enough space before hashing', async () => {
		const { m, t, hashSpy } = setup();
		t.preflightStatus = () => ({ status: 'insufficient_space', required: 7_300_000_000, available: 5_100_000_000 });
		const [item] = await m.add([{ file: file(10), place: 'p', path: '/big' }]);
		await settle();
		expect(item.state).toBe('failed');
		expect(item.message).toBe('Not enough space on p: needs 7.3 GB, 5.1 GB free');
		expect(hashSpy).not.toHaveBeenCalled();
	});

	it('cancels a registered upload on the server', async () => {
		const { m, t } = setup();
		t.auto = false;
		const [item] = await m.add([{ file: file(2 * CHUNK_SIZE), place: 'p', path: '/x' }]);
		await settle();
		m.cancel(item);
		await settle();
		expect(item.state).toBe('cancelled');
		expect(t.cancelled).toEqual([item.serverId]);
	});

	it('retries a chunk in progress after 2 s', async () => {
		const { m, t } = setup();
		let first = true;
		t.chunkResult = (p) => {
			if (first) {
				first = false;
				return { status: 409, body: { error: { code: 'chunk_in_progress' } } };
			}
			return p.final ? { status: 200, body: {} } : { status: 200, body: {} };
		};
		const [item] = await m.add([{ file: file(10), place: 'p', path: '/a' }]);
		await settle();
		expect(t.log.length).toBe(1);
		await vi.advanceTimersByTimeAsync(1999);
		expect(t.log.length).toBe(1);
		await vi.advanceTimersByTimeAsync(1);
		expect(item.state).toBe('done');
	});

	it('completes empty files at registration', async () => {
		const { m, t } = setup();
		t.registerResult = () => ({ status: 200, body: { status: 'complete', entry: { name: 'e' } } });
		const [item] = await m.add([{ file: file(0), place: 'p', path: '/e' }]);
		await settle();
		expect(item.state).toBe('done');
		expect(t.log).toEqual([]);
	});

	it('notes when a leftover resume started from the beginning', async () => {
		const { m } = setup();
		const items: UploadItem[] = await m.add([{ file: file(5), place: 'p', path: '/l' }], { expectResume: true });
		await settle();
		expect(items[0].note).toMatch(/Content differed/);
	});
});
