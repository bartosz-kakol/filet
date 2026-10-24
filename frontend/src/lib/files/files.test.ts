import { describe, expect, it } from 'vitest';
import type { Entry } from '$lib/api/types';
import { readPref, isSort, isTheme, isConcurrency, type KV } from '$lib/prefs/storage';
import { getViewMode, setViewMode, MAX_VIEW_MODES } from '$lib/prefs/viewmodes';
import { badgeText, formatBytes } from './format';
import { filterHidden, sortEntries } from './sort';
import { validateName } from './path';

const e = (name: string, type: Entry['type'] = 'file', size = 0, mtime = 0, hidden = name.startsWith('.')) =>
	({ name, type, size, mtime, hidden }) as Entry;

function memKV(): KV & { data: Map<string, string> } {
	const data = new Map<string, string>();
	return { data, getItem: (k) => data.get(k) ?? null, setItem: (k, v) => void data.set(k, v) };
}

describe('sorting and filtering', () => {
	const list = [e('file10.txt', 'file', 30, 3), e('file2.txt', 'file', 10, 1), e('Zeta', 'dir'), e('alpha', 'dir'), e('b.md', 'file', 20, 2)];
	it('natural name order with directories first', () => {
		expect(sortEntries(list, { key: 'name', dir: 'asc' }).map((x) => x.name)).toEqual(['alpha', 'Zeta', 'b.md', 'file2.txt', 'file10.txt']);
		expect(sortEntries(list, { key: 'name', dir: 'desc' }).map((x) => x.name)).toEqual(['Zeta', 'alpha', 'file10.txt', 'file2.txt', 'b.md']);
	});
	it('size, date and type', () => {
		expect(sortEntries(list, { key: 'size', dir: 'asc' }).slice(2).map((x) => x.name)).toEqual(['file2.txt', 'b.md', 'file10.txt']);
		expect(sortEntries(list, { key: 'mtime', dir: 'desc' }).slice(2).map((x) => x.name)).toEqual(['file10.txt', 'b.md', 'file2.txt']);
		expect(sortEntries(list, { key: 'type', dir: 'asc' }).slice(2).map((x) => x.name)).toEqual(['b.md', 'file2.txt', 'file10.txt']);
	});
	it('hides dotfiles unless enabled', () => {
		const l = [e('.env'), e('a')];
		expect(filterHidden(l, false).map((x) => x.name)).toEqual(['a']);
		expect(filterHidden(l, true)).toHaveLength(2);
	});
});

describe('view modes per directory', () => {
	it('are exact-directory only and never inherited', () => {
		const kv = memKV();
		setViewMode('media', '/A', 'grid', kv);
		expect(getViewMode('media', '/A', kv)).toBe('grid');
		expect(getViewMode('media', '/A/B', kv)).toBe('list');
		expect(getViewMode('other', '/A', kv)).toBe('list');
	});
	it('evict least-recently-used entries above 5,000', () => {
		const kv = memKV();
		const seed: Record<string, { m: string; t: number }> = {};
		for (let i = 0; i < MAX_VIEW_MODES; i++) seed[`p:/d${i}`] = { m: 'list', t: i + 1 };
		kv.data.set('filet.viewModes', JSON.stringify(seed));
		setViewMode('p', `/d${MAX_VIEW_MODES}`, 'grid', kv, MAX_VIEW_MODES + 1);
		const map = JSON.parse(kv.data.get('filet.viewModes')!);
		expect(Object.keys(map)).toHaveLength(MAX_VIEW_MODES);
		expect(map['p:/d0']).toBeUndefined();
		expect(map[`p:/d${MAX_VIEW_MODES}`]).toBeDefined();
	});
});

describe('preferences', () => {
	it('fall back to defaults on corrupt values', () => {
		const kv = memKV();
		kv.data.set('filet.theme', '"purple"');
		kv.data.set('filet.sort', '{not json');
		kv.data.set('filet.uploadConcurrency', '99');
		kv.data.set('filet.viewModes', '[1,2]');
		expect(readPref('filet.theme', 'system', isTheme, kv)).toBe('system');
		expect(readPref('filet.sort', { key: 'name', dir: 'asc' }, isSort, kv)).toEqual({ key: 'name', dir: 'asc' });
		expect(readPref('filet.uploadConcurrency', 4, isConcurrency, kv)).toBe(4);
		expect(getViewMode('p', '/', kv)).toBe('list');
		kv.data.set('filet.theme', '"dark"');
		expect(readPref('filet.theme', 'system', isTheme, kv)).toBe('dark');
	});
});

describe('formatting', () => {
	it('leftover badge', () => {
		expect(badgeText(0)).toBeNull();
		expect(badgeText(1)).toBe('1');
		expect(badgeText(9)).toBe('9');
		expect(badgeText(10)).toBe('9+');
	});
	it('bytes', () => {
		expect(formatBytes(999)).toBe('999 B');
		expect(formatBytes(2_400_000)).toBe('2.4 MB');
		expect(formatBytes(7_300_000_000)).toBe('7.3 GB');
	});
	it('validates names', () => {
		expect(validateName('ok.txt')).toBeNull();
		for (const bad of ['', 'a/b', '.filet', '.filet-part-1', '.filet-copy-x', 'x'.repeat(256), '..']) expect(validateName(bad)).not.toBeNull();
	});
});
