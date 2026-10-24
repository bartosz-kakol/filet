import { get } from '$lib/api/client';

class LeftoverCount {
	count = $state(0);
	#timer: ReturnType<typeof setInterval> | null = null;

	async refresh() {
		try {
			this.count = (await get<{ count: number }>('/api/uploads/leftovers/count')).count;
		} catch {
			/* keep the last value */
		}
	}

	start() {
		if (this.#timer) return;
		void this.refresh();
		this.#timer = setInterval(() => void this.refresh(), 60_000);
	}
}

export const leftovers = new LeftoverCount();
