import type { Source } from '$lib/api/source';
import type { Entry, MediaInfo } from '$lib/api/types';
import { joinPath } from '$lib/files/path';
import { decidePlayback } from './media';
import { SMALL_TEXT_LIMIT } from './sniff';

interface Slot {
	name: string;
	abort: AbortController;
	img?: HTMLImageElement;
	media?: HTMLMediaElement;
}

const textCache = new Map<string, Promise<string>>();
const infoCache = new Map<string, Promise<unknown>>();

/** Cached full-text fetch shared with the viewers (bounded to the 3 slots). */
export function fetchText(url: string, signal?: AbortSignal): Promise<string> {
	let p = textCache.get(url);
	if (!p) {
		p = fetch(url, { credentials: 'same-origin', signal }).then((r) => {
			if (!r.ok) throw new Error(`HTTP ${r.status}`);
			return r.text();
		});
		p.catch(() => textCache.delete(url));
		textCache.set(url, p);
	}
	return p;
}

/** Cached media-info fetch. */
export function fetchInfo<T>(url: string, signal?: AbortSignal): Promise<T> {
	let p = infoCache.get(url) as Promise<T> | undefined;
	if (!p) {
		p = fetch(url, { credentials: 'same-origin', signal }).then(async (r) => {
			if (!r.ok) {
				const b = await r.json().catch(() => null);
				throw new Error(b?.error?.message ?? `HTTP ${r.status}`);
			}
			return r.json() as Promise<T>;
		});
		p.catch(() => infoCache.delete(url));
		infoCache.set(url, p);
		if (infoCache.size > 64) infoCache.delete(infoCache.keys().next().value!);
	}
	return p;
}

/**
 * Prefetches the previous and next items. Exactly three slots exist (prev,
 * current, next); leaving a slot aborts its fetches and drops cached data.
 */
export class Prefetcher {
	#slots = new Map<string, Slot>();

	constructor(private src: Source) {}

	update(dir: string, current: Entry, prev: Entry | undefined, next: Entry | undefined) {
		const keep = new Set([current.name, prev?.name, next?.name].filter(Boolean) as string[]);
		for (const [name, slot] of this.#slots) {
			if (!keep.has(name)) {
				slot.abort.abort();
				if (slot.img) slot.img.src = '';
				if (slot.media) {
					slot.media.removeAttribute('src');
					slot.media.load();
				}
				textCache.delete(this.src.raw(joinPath(dir, name)));
				this.#slots.delete(name);
			}
		}
		for (const e of [prev, next]) {
			if (e && !this.#slots.has(e.name)) this.#slots.set(e.name, this.#start(dir, e));
		}
		if (!this.#slots.has(current.name)) this.#slots.set(current.name, { name: current.name, abort: new AbortController() });
	}

	#start(dir: string, e: Entry): Slot {
		const path = joinPath(dir, e.name);
		const slot: Slot = { name: e.name, abort: new AbortController() };
		switch (e.kind) {
			case 'image':
			case 'image-render': {
				const img = new Image();
				img.decoding = 'async';
				img.src = e.kind === 'image' ? this.src.raw(path) : this.src.render(path, e);
				img.decode().catch(() => {});
				slot.img = img;
				break;
			}
			case 'video':
			case 'audio':
				fetchInfo<MediaInfo>(this.src.mediaInfo(path), slot.abort.signal)
					.then((info) => {
						if (slot.abort.signal.aborted) return;
						const el = document.createElement(e.kind === 'video' ? 'video' : 'audio');
						if (decidePlayback(e.name, info, (t) => el.canPlayType(t)).mode !== 'direct') return;
						// Direct-play files: warm up the container metadata.
						el.preload = 'metadata';
						el.muted = true;
						el.src = this.src.raw(path);
						slot.media = el;
					})
					.catch(() => {});
				break;
			case 'text':
			case 'markdown':
			case 'csv':
				if (e.size <= SMALL_TEXT_LIMIT) fetchText(this.src.raw(path), slot.abort.signal).catch(() => {});
				break;
		}
		return slot;
	}

	dispose() {
		for (const s of this.#slots.values()) {
			s.abort.abort();
			if (s.img) s.img.src = '';
		}
		this.#slots.clear();
	}
}
