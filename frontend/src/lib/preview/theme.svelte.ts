/** Tracks whether the app (the <html> element) is in dark mode. */
class AppTheme {
	dark = $state(false);

	constructor() {
		if (typeof document === 'undefined') return;
		const html = document.documentElement;
		this.dark = html.classList.contains('dark');
		new MutationObserver(() => (this.dark = html.classList.contains('dark'))).observe(html, {
			attributes: true,
			attributeFilter: ['class']
		});
	}
}

export const appTheme = new AppTheme();
