/** Reactive viewport facts. */
class Viewport {
	mobile = $state(false);
	coarse = $state(false);

	constructor() {
		if (typeof window === 'undefined') return;
		const m = window.matchMedia('(max-width: 767px)');
		const c = window.matchMedia('(pointer: coarse)');
		this.mobile = m.matches;
		this.coarse = c.matches;
		m.addEventListener('change', (e) => (this.mobile = e.matches));
		c.addEventListener('change', (e) => (this.coarse = e.matches));
	}
}

export const viewport = new Viewport();
