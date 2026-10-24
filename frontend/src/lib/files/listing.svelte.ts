import { get } from '$lib/api/client';
import type { Source } from '$lib/api/source';
import type { Listing } from '$lib/api/types';

interface Cached {
	listing: Listing;
	at: number;
}

/**
 * In-memory listing cache keyed by "<source base>:<path>". Navigation shows
 * the cached listing at once and revalidates in the background.
 */
class ListingCache {
	#map = new Map<string, Cached>();
	#inflight = new Map<string, Promise<Listing>>();

	key(src: Source, path: string) {
		return `${src.base}:${path}`;
	}

	peek(src: Source, path: string): Cached | undefined {
		return this.#map.get(this.key(src, path));
	}

	fetch(src: Source, path: string, signal?: AbortSignal): Promise<Listing> {
		const k = this.key(src, path);
		let p = this.#inflight.get(k);
		if (!p) {
			p = get<Listing>(src.list(path), { signal, noAuthRedirect: src.isPublic })
				.then((l) => {
					this.#map.set(k, { listing: l, at: Date.now() });
					if (this.#map.size > 500) this.#map.delete(this.#map.keys().next().value!);
					return l;
				})
				.finally(() => this.#inflight.delete(k));
			this.#inflight.set(k, p);
		}
		return p;
	}

	/** Prefetch for hover; errors are ignored. */
	prefetch(src: Source, path: string) {
		if (this.peek(src, path)) return;
		this.fetch(src, path).catch(() => {});
	}

	invalidate(src: Source, path: string) {
		this.#map.delete(this.key(src, path));
	}
}

export const listingCache = new ListingCache();
