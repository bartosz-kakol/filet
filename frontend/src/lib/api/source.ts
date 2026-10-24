import { encPath } from './client';
import type { Entry } from './types';

/**
 * A Source is where browse/preview requests go: a place for signed-in
 * users (/api/places/<id>) or a public share (/api/public/share/<token>).
 * Both expose the same sub-resources.
 */
export class Source {
	constructor(
		public base: string,
		public isPublic: boolean,
		public placeId: string | null,
		public label: string
	) {}

	static #cache = new Map<string, Source>();

	/** Returns a stable instance per place so reactive consumers don't reload. */
	static place(id: string, name: string) {
		const key = `p:${id}:${name}`;
		let s = Source.#cache.get(key);
		if (!s) Source.#cache.set(key, (s = new Source(`/api/places/${encodeURIComponent(id)}`, false, id, name)));
		return s;
	}

	static share(token: string, name: string) {
		const key = `s:${token}:${name}`;
		let s = Source.#cache.get(key);
		if (!s) Source.#cache.set(key, (s = new Source(`/api/public/share/${encodeURIComponent(token)}`, true, null, name)));
		return s;
	}

	list(path: string) {
		return `${this.base}/list?path=${encodeURIComponent(path)}`;
	}
	raw(path: string, download = false) {
		return `${this.base}/raw/${encPath(path)}${download ? '?download=1' : ''}`;
	}
	thumb(path: string, e: Pick<Entry, 'mtime' | 'size'>) {
		return `${this.base}/thumb/${encPath(path)}?v=${e.mtime}-${e.size}`;
	}
	render(path: string, e: Pick<Entry, 'mtime' | 'size'>) {
		return `${this.base}/render/${encPath(path)}?v=${e.mtime}-${e.size}`;
	}
	mediaInfo(path: string) {
		return `${this.base}/media/info/${encPath(path)}`;
	}
	remux(path: string, start: number, video: number, audio: number) {
		return `${this.base}/media/remux/${encPath(path)}?start=${start.toFixed(3)}&video=${video}&audio=${audio}`;
	}
	subtitle(path: string, q: { stream?: number; sidecar?: string }) {
		const qs = q.sidecar !== undefined ? `sidecar=${encodeURIComponent(q.sidecar)}` : `stream=${q.stream}`;
		return `${this.base}/media/subtitle/${encPath(path)}?${qs}`;
	}
	textIndex(path: string) {
		return `${this.base}/text-index/${encPath(path)}`;
	}
	archiveList(path: string) {
		return `${this.base}/archive/list/${encPath(path)}`;
	}
	archiveEntry(path: string, entry: string, download = false) {
		return `${this.base}/archive/entry/${encPath(path)}?entry=${encodeURIComponent(entry)}&download=${download ? 1 : 0}`;
	}
	zip() {
		return `${this.base}/zip`;
	}
}
