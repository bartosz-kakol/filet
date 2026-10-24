import type { Entry } from '$lib/api/types';
import type { Sort } from '$lib/prefs/storage';
import { ext } from './kinds';

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

export function compareNames(a: string, b: string): number {
	return collator.compare(a, b);
}

/** Sorts entries: directories first, then by the chosen key and direction. */
export function sortEntries<T extends Pick<Entry, 'name' | 'type' | 'size' | 'mtime'>>(entries: T[], sort: Sort): T[] {
	const dir = sort.dir === 'asc' ? 1 : -1;
	const isDir = (e: T) => (e.type === 'dir' ? 0 : 1);
	return [...entries].sort((a, b) => {
		const d = isDir(a) - isDir(b);
		if (d !== 0) return d;
		let c = 0;
		switch (sort.key) {
			case 'size':
				c = a.size - b.size;
				break;
			case 'mtime':
				c = a.mtime - b.mtime;
				break;
			case 'type':
				c = collator.compare(ext(a.name), ext(b.name));
				break;
		}
		if (c === 0) c = collator.compare(a.name, b.name);
		return c * dir;
	});
}

/** Applies the dotfile toggle. */
export function filterHidden<T extends Pick<Entry, 'hidden'>>(entries: T[], showHidden: boolean): T[] {
	return showHidden ? entries : entries.filter((e) => !e.hidden);
}
