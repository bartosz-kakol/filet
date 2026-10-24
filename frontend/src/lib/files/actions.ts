import type { Source } from '$lib/api/source';
import type { Entry } from '$lib/api/types';
import type { Picked } from '$lib/upload/sources';
import type { NewUpload } from '$lib/upload/manager.svelte';
import { joinPath } from './path';

/** Starts a native download of one file. */
export function downloadFile(src: Source, path: string) {
	const a = document.createElement('a');
	a.href = src.raw(path, true);
	a.download = '';
	a.rel = 'noopener';
	document.body.appendChild(a);
	a.click();
	a.remove();
}

/** Streams a zip of dir/names through a hidden same-origin form POST. */
export function downloadZip(src: Source, dir: string, names: string[]) {
	const form = document.createElement('form');
	form.method = 'POST';
	form.action = src.zip();
	form.style.display = 'none';
	const add = (k: string, v: string) => {
		const i = document.createElement('input');
		i.type = 'hidden';
		i.name = k;
		i.value = v;
		form.appendChild(i);
	};
	add('dir', dir);
	for (const n of names) add('name', n);
	document.body.appendChild(form);
	form.submit();
	form.remove();
}

/** Downloads a selection: a direct download for one file, a zip otherwise. */
export function downloadSelection(src: Source, dir: string, entries: Entry[]) {
	if (entries.length === 1 && entries[0].type === 'file') downloadFile(src, joinPath(dir, entries[0].name));
	else if (entries.length) downloadZip(src, dir, entries.map((e) => e.name));
}

/** Maps picked files to upload requests under a target directory. */
export function planUpload(place: string, targetDir: string, picked: Picked): { uploads: NewUpload[]; dirs: { place: string; path: string }[] } {
	return {
		uploads: picked.files.map((f) => ({ file: f.file, place, path: joinPath(targetDir, f.rel) })),
		dirs: picked.dirs.map((d) => ({ place, path: joinPath(targetDir, d) }))
	};
}

/** Data type for Filet-internal drags. */
export const FILET_DRAG = 'application/x-filet-items';

export interface DragPayload {
	place: string;
	paths: string[];
}

export function canOpen(e: Entry): boolean {
	return e.readable && !e.error && !e.badName && (!e.symlink || e.symlink.status === 'ok');
}
