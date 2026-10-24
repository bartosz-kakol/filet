import { readPref, writePref, type KV } from './storage';

// Per-directory list/grid memory: exact directory only, never inherited.

export type ViewMode = 'list' | 'grid';
export type ViewModeMap = Record<string, { m: ViewMode; t: number }>;

const KEY = 'filet.viewModes';
export const MAX_VIEW_MODES = 5000;

const isMap = (v: unknown): v is ViewModeMap =>
	typeof v === 'object' &&
	v !== null &&
	!Array.isArray(v) &&
	Object.values(v as object).every(
		(e) => typeof e === 'object' && e !== null && (e.m === 'list' || e.m === 'grid') && typeof e.t === 'number'
	);

export function dirKey(place: string, path: string) {
	return `${place}:${path}`;
}

export function getViewMode(place: string, path: string, kv?: KV | null): ViewMode {
	const map = readPref<ViewModeMap>(KEY, {}, isMap, kv);
	return map[dirKey(place, path)]?.m ?? 'list';
}

/** Records the mode for one directory, evicting least-recently-used entries. */
export function setViewMode(place: string, path: string, mode: ViewMode, kv?: KV | null, now = Date.now()) {
	const map = readPref<ViewModeMap>(KEY, {}, isMap, kv);
	map[dirKey(place, path)] = { m: mode, t: now };
	const keys = Object.keys(map);
	if (keys.length > MAX_VIEW_MODES) {
		keys.sort((a, b) => map[a].t - map[b].t);
		for (const k of keys.slice(0, keys.length - MAX_VIEW_MODES)) delete map[k];
	}
	writePref(KEY, map, kv);
}
