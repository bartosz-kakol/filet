export const CHUNK_SIZE = 8 * 1024 * 1024;
/** Chunks in flight per file. */
export const PER_FILE = 3;
/** Chunks in flight across all files. */
export const GLOBAL = 8;
export const STALL_MS = 60_000;
export const BACKOFF_S = [1, 2, 4, 8, 16, 30];
export const IN_PROGRESS_RETRY_MS = 2000;
export const PREFLIGHT_BATCH = 10_000;

export function chunkCount(size: number): number {
	return Math.ceil(size / CHUNK_SIZE);
}

export function chunkRange(size: number, i: number): [number, number] {
	const start = i * CHUNK_SIZE;
	return [start, Math.min(start + CHUNK_SIZE, size)];
}

/** Backoff delay (ms) for the n-th consecutive transient failure (1-based). */
export function backoffMs(n: number): number {
	return BACKOFF_S[Math.min(n, BACKOFF_S.length) - 1] * 1000;
}

export function hasherCount(): number {
	const hc = typeof navigator !== 'undefined' ? navigator.hardwareConcurrency || 2 : 2;
	return Math.min(4, Math.max(1, hc - 1));
}

/** Expands inclusive [a, b] ranges into indexes. */
export function expandRanges(ranges: [number, number][]): number[] {
	const out: number[] = [];
	for (const [a, b] of ranges) for (let i = a; i <= b; i++) out.push(i);
	return out;
}

export function toB64Url(bytes: Uint8Array): string {
	let s = '';
	const step = 0x8000;
	for (let i = 0; i < bytes.length; i += step) s += String.fromCharCode(...bytes.subarray(i, i + step));
	return btoa(s).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
