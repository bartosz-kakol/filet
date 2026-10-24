import { createBLAKE3 } from 'hash-wasm';
import { describe, expect, it } from 'vitest';
import vectors from './testdata/blake3.json';
import { HasherPool } from './hasher';
import { CHUNK_SIZE } from './constants';

const hex = (b: Uint8Array) => Buffer.from(b).toString('hex');

describe('BLAKE3 (hash-wasm) matches zeebo/blake3 on the server', () => {
	it('known vectors', async () => {
		const h = await createBLAKE3(256);
		const pat = new Uint8Array(8 * 1024 * 1024);
		for (let i = 0; i < pat.length; i++) pat[i] = i % 251;
		expect(hex(h.init().update(new Uint8Array()).digest('binary'))).toBe(vectors.empty);
		expect(hex(h.init().update(new TextEncoder().encode('abc')).digest('binary'))).toBe(vectors.abc);
		expect(hex(h.init().update(pat).digest('binary'))).toBe(vectors.pattern8MiB);
	});

	it('the worker pool hashes chunks in order', async () => {
		// In-process stand-in for the Web Worker (same message protocol).
		const factory = () => {
			const w = {
				onmessage: null as ((e: MessageEvent) => void) | null,
				async postMessage(m: { taskId: number; file: Blob; start: number; end: number }) {
					const h = await createBLAKE3(256);
					const bytes = new Uint8Array(await m.file.slice(m.start, m.end).arrayBuffer());
					const digest = h.init().update(bytes).digest('binary');
					setTimeout(() => w.onmessage?.({ data: { taskId: m.taskId, digest } } as MessageEvent));
				}
			};
			return w;
		};
		const pool = new HasherPool(2, factory);
		const data = new Uint8Array(2 * CHUNK_SIZE + 10);
		for (let i = 0; i < data.length; i++) data[i] = i % 251;
		let progress = 0;
		const out = await pool.hash(new Blob([data]), (n) => (progress = n), new AbortController().signal);
		expect(out.length).toBe(96);
		expect(hex(out.subarray(0, 32))).toBe(vectors.pattern8MiB);
		expect(progress).toBe(data.length);
		expect((await pool.hash(new Blob([]), () => {}, new AbortController().signal)).length).toBe(0);
	});
});
