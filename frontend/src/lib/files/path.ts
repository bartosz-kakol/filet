export function joinPath(dir: string, name: string): string {
	return dir === '/' ? '/' + name : dir + '/' + name;
}

export function parentPath(p: string): string {
	if (p === '/') return '/';
	const i = p.lastIndexOf('/');
	return i <= 0 ? '/' : p.slice(0, i);
}

export function baseName(p: string): string {
	return p.slice(p.lastIndexOf('/') + 1);
}

/** Splits "/a/b" into ["a", "b"]. */
export function segments(p: string): string[] {
	return p.split('/').filter(Boolean);
}

/** "/a/b" → URL path "a/b" with each segment encoded. */
export function encodeSegments(p: string): string {
	return segments(p).map(encodeURIComponent).join('/');
}

/** Builds the browser route for a directory. */
export function dirHref(place: string, dir: string, query?: Record<string, string>): string {
	const qs = query ? '?' + new URLSearchParams(query).toString() : '';
	const tail = encodeSegments(dir);
	return `/p/${encodeURIComponent(place)}${tail ? '/' + tail : ''}${qs}`;
}

/** True when `p` is `dir` or below it. */
export function isWithin(p: string, dir: string): boolean {
	return dir === '/' || p === dir || p.startsWith(dir + '/');
}

const RESERVED = /^(\.filet|\.filet-part-.*|\.filet-copy-.*)$/;

/** Inline validation for new names; returns an error message or null. */
export function validateName(name: string): string | null {
	if (name.trim() === '') return 'Name cannot be empty';
	if (name.includes('/')) return 'Name cannot contain “/”';
	if (name === '.' || name === '..') return 'Invalid name';
	if (RESERVED.test(name)) return 'This name is reserved by Filet';
	if (new TextEncoder().encode(name).length > 255) return 'Name is too long';
	return null;
}
