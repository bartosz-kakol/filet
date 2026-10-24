/// <reference lib="webworker" />
import { createBLAKE3, type IHasher } from 'hash-wasm';

// One BLAKE3-256 hasher per worker, reused for every chunk.
let hasher: IHasher | null = null;

export interface HashTask {
	taskId: number;
	file: Blob;
	start: number;
	end: number;
}

self.onmessage = async (e: MessageEvent<HashTask>) => {
	const { taskId, file, start, end } = e.data;
	try {
		hasher ??= await createBLAKE3(256);
		const bytes = new Uint8Array(await file.slice(start, end).arrayBuffer());
		const digest = hasher.init().update(bytes).digest('binary');
		(self as unknown as DedicatedWorkerGlobalScope).postMessage({ taskId, digest }, [digest.buffer]);
	} catch (err) {
		(self as unknown as DedicatedWorkerGlobalScope).postMessage({ taskId, error: String(err) });
	}
};
