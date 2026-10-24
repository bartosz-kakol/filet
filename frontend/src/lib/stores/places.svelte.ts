import { get } from '$lib/api/client';
import type { Place, Settings } from '$lib/api/types';

class Places {
	list = $state<Place[]>([]);
	loaded = $state(false);
	settings = $state<Settings | null>(null);
	#inflight: Promise<void> | null = null;

	byId(id: string | null | undefined): Place | undefined {
		return this.list.find((p) => p.id === id);
	}

	name(id: string): string {
		return this.byId(id)?.name ?? id;
	}

	refresh(): Promise<void> {
		// Callers fire and forget; a failure (offline, navigation) keeps the
		// last known list.
		this.#inflight ??= get<Place[]>('/api/places')
			.then((l) => {
				this.list = l;
				this.loaded = true;
			})
			.catch(() => {})
			.finally(() => (this.#inflight = null));
		return this.#inflight;
	}

	async loadSettings() {
		if (!this.settings) this.settings = await get<Settings>('/api/settings');
		return this.settings;
	}
}

export const places = new Places();
