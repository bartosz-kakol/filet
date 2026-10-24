// Thin typed fetch wrappers. Every non-2xx response becomes an ApiError.

export class ApiError extends Error {
	constructor(
		public status: number,
		public code: string,
		message: string,
		public details: Record<string, unknown> = {}
	) {
		super(message);
		this.name = 'ApiError';
	}
}

type UnauthorizedHandler = () => void;
let onUnauthorized: UnauthorizedHandler | null = null;

/** Registers the global 401 handler (re-login dialog or redirect). */
export function setUnauthorizedHandler(h: UnauthorizedHandler | null) {
	onUnauthorized = h;
}

export function notifyUnauthorized() {
	onUnauthorized?.();
}

export async function toApiError(res: Response): Promise<ApiError> {
	let code = 'http_' + res.status;
	let message = res.statusText || 'Request failed';
	let details: Record<string, unknown> = {};
	try {
		const body = await res.json();
		if (body?.error) {
			code = body.error.code ?? code;
			message = body.error.message ?? message;
			details = body.error.details ?? {};
		}
	} catch {
		/* not JSON */
	}
	return new ApiError(res.status, code, message, details);
}

export interface RequestOptions {
	signal?: AbortSignal;
	/** Don't run the global 401 handler (login/session/public endpoints). */
	noAuthRedirect?: boolean;
	headers?: Record<string, string>;
}

export async function request<T = unknown>(
	method: string,
	url: string,
	body?: unknown,
	opts: RequestOptions = {}
): Promise<T> {
	const headers: Record<string, string> = { ...opts.headers };
	let payload: BodyInit | undefined;
	if (body !== undefined) {
		headers['Content-Type'] = 'application/json';
		payload = JSON.stringify(body);
	}
	const res = await fetch(url, { method, body: payload, headers, credentials: 'same-origin', signal: opts.signal });
	if (!res.ok) {
		const err = await toApiError(res);
		if (res.status === 401 && err.code === 'unauthenticated' && !opts.noAuthRedirect) notifyUnauthorized();
		throw err;
	}
	if (res.status === 204) return undefined as T;
	const ct = res.headers.get('Content-Type') ?? '';
	if (ct.includes('application/json')) return (await res.json()) as T;
	return (await res.text()) as T;
}

export const get = <T>(url: string, opts?: RequestOptions) => request<T>('GET', url, undefined, opts);
export const post = <T>(url: string, body?: unknown, opts?: RequestOptions) => request<T>('POST', url, body ?? {}, opts);
export const del = <T>(url: string, opts?: RequestOptions) => request<T>('DELETE', url, undefined, opts);

/** Encodes a place path ("/a b/c") as URL segments ("a%20b/c"). */
export function encPath(path: string): string {
	return path
		.split('/')
		.filter((s) => s !== '')
		.map(encodeURIComponent)
		.join('/');
}

/** Human message for an error. */
export function errorMessage(e: unknown): string {
	if (e instanceof ApiError) return e.message;
	if (e instanceof Error) return e.message;
	return String(e);
}
