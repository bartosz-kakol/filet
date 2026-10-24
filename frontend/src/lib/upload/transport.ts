import type { Entry } from '$lib/api/types';
import { STALL_MS } from './constants';

export type PreflightStatus =
	| 'ok'
	| 'resumable'
	| 'conflict'
	| 'uploading'
	| 'insufficient_space'
	| 'readonly'
	| 'invalid_path'
	| 'reserved_name'
	| 'parent_not_directory'
	| 'permission_denied'
	| 'place_not_found';

export interface PreflightItem {
	key: string;
	place: string;
	path: string;
	size: number;
}

export interface PreflightResult {
	key: string;
	status: PreflightStatus;
	existing?: Entry;
	suggestedName?: string;
	required?: number;
	available?: number;
	receivedBytes?: number;
	replacesLeftover?: boolean;
}

export interface RegisterRequest {
	place: string;
	path: string;
	size: number;
	mtime: number;
	chunkSize: number;
	hashes: string;
	conflict: 'fail' | 'overwrite';
}

/** An HTTP outcome; status 0 means a network error, abort or stall. */
export interface HttpResult {
	status: number;
	// eslint-disable-next-line @typescript-eslint/no-explicit-any
	body: any;
}

export interface Transport {
	preflight(items: PreflightItem[]): Promise<HttpResult>;
	register(req: RegisterRequest): Promise<HttpResult>;
	putChunk(
		id: string,
		index: number,
		blob: Blob,
		final: boolean,
		onProgress: (loaded: number) => void,
		signal: AbortSignal
	): Promise<HttpResult>;
	cancel(id: string): Promise<void>;
	mkdir(place: string, path: string): Promise<HttpResult>;
}

async function fetchJSON(method: string, url: string, body?: unknown): Promise<HttpResult> {
	try {
		const res = await fetch(url, {
			method,
			credentials: 'same-origin',
			headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
			body: body === undefined ? undefined : JSON.stringify(body)
		});
		let parsed: unknown = null;
		const text = await res.text();
		try {
			parsed = text ? JSON.parse(text) : null;
		} catch {
			parsed = text;
		}
		return { status: res.status, body: parsed };
	} catch {
		return { status: 0, body: null };
	}
}

/** Extra request headers, e.g. for fault injection in end-to-end tests. */
export const testHeaders: Record<string, string> = {};

export const httpTransport: Transport = {
	preflight: (items) => fetchJSON('POST', '/api/uploads/preflight', { items }),
	register: (req) => fetchJSON('POST', '/api/uploads', req),
	mkdir: (place, path) => fetchJSON('POST', `/api/places/${encodeURIComponent(place)}/mkdir`, { path, parents: true }),
	async cancel(id) {
		await fetchJSON('DELETE', `/api/uploads/${encodeURIComponent(id)}`);
	},
	putChunk(id, index, blob, final, onProgress, signal) {
		return new Promise((resolve) => {
			const xhr = new XMLHttpRequest();
			let stall: ReturnType<typeof setTimeout>;
			let settled = false;
			const finish = (r: HttpResult) => {
				if (settled) return;
				settled = true;
				clearTimeout(stall);
				signal.removeEventListener('abort', onAbort);
				resolve(r);
			};
			const armStall = () => {
				clearTimeout(stall);
				stall = setTimeout(() => {
					xhr.abort();
					finish({ status: 0, body: { stalled: true } });
				}, STALL_MS);
			};
			const onAbort = () => {
				xhr.abort();
				finish({ status: 0, body: { aborted: true } });
			};
			signal.addEventListener('abort', onAbort, { once: true });
			xhr.open('PUT', `/api/uploads/${encodeURIComponent(id)}/chunks/${index}`);
			xhr.setRequestHeader('Content-Type', 'application/octet-stream');
			if (final) xhr.setRequestHeader('Filet-Final', '1');
			for (const [k, v] of Object.entries(testHeaders)) xhr.setRequestHeader(k, v);
			xhr.upload.onprogress = (e) => {
				armStall();
				onProgress(e.loaded);
			};
			xhr.onload = () => {
				let body: unknown = null;
				try {
					body = xhr.responseText ? JSON.parse(xhr.responseText) : null;
				} catch {
					body = xhr.responseText;
				}
				finish({ status: xhr.status, body });
			};
			xhr.onerror = () => finish({ status: 0, body: null });
			xhr.onabort = () => finish({ status: 0, body: { aborted: true } });
			armStall();
			xhr.send(blob);
		});
	}
};
