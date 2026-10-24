// Validated localStorage access. Every read falls back to the default on a
// missing, unparsable or wrongly shaped value.

export interface KV {
	getItem(k: string): string | null;
	setItem(k: string, v: string): void;
}

function store(): KV | null {
	try {
		return typeof localStorage === 'undefined' ? null : localStorage;
	} catch {
		return null;
	}
}

export function readPref<T>(key: string, def: T, valid: (v: unknown) => v is T, kv: KV | null = store()): T {
	if (!kv) return def;
	try {
		const raw = kv.getItem(key);
		if (raw === null) return def;
		const v: unknown = JSON.parse(raw);
		return valid(v) ? v : def;
	} catch {
		return def;
	}
}

export function writePref(key: string, value: unknown, kv: KV | null = store()) {
	try {
		kv?.setItem(key, JSON.stringify(value));
	} catch {
		/* quota or private mode */
	}
}

export type Theme = 'system' | 'light' | 'dark';
export type SortKey = 'name' | 'size' | 'mtime' | 'type';
export interface Sort {
	key: SortKey;
	dir: 'asc' | 'desc';
}

export const isTheme = (v: unknown): v is Theme => v === 'system' || v === 'light' || v === 'dark';
export const isBool = (v: unknown): v is boolean => typeof v === 'boolean';
export const isString = (v: unknown): v is string => typeof v === 'string' && v.length > 0;
export const isSort = (v: unknown): v is Sort =>
	typeof v === 'object' &&
	v !== null &&
	['name', 'size', 'mtime', 'type'].includes((v as Sort).key) &&
	((v as Sort).dir === 'asc' || (v as Sort).dir === 'desc');
export const isConcurrency = (v: unknown): v is number => Number.isInteger(v) && (v as number) >= 1 && (v as number) <= 16;
export const isMarkdownMode = (v: unknown): v is 'rendered' | 'raw' => v === 'rendered' || v === 'raw';
