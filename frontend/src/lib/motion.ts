// Motion helpers. CSS animations live in src/motion.css (tagged [Mxx]); the
// pieces here run in JS, so they check prefers-reduced-motion themselves.
import { fade, fly, slide, type FadeParams, type FlyParams, type SlideParams } from 'svelte/transition';
import { cubicOut } from 'svelte/easing';

export const reducedMotion = () =>
	typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;

const still = <P extends { duration?: number }>(p: P | undefined): P => (reducedMotion() ? { ...p, duration: 0 } : { ...p }) as P;

export const mfade = (node: Element, p?: FadeParams) => fade(node, still(p));
export const mfly = (node: Element, p?: FlyParams) => fly(node, still({ easing: cubicOut, ...p }));
export const mslide = (node: Element, p?: SlideParams) => slide(node, still({ easing: cubicOut, ...p }));

/** [M04] One delegated listener spawns a ripple inside any .btn or [data-ripple] element. */
export function installRipple(): () => void {
	const onDown = (e: PointerEvent) => {
		if (e.button !== 0 || reducedMotion()) return;
		const host = (e.target as Element | null)?.closest<HTMLElement>('.btn, [data-ripple]');
		if (!host || (host as HTMLButtonElement).disabled) return;
		const r = host.getBoundingClientRect();
		const size = Math.hypot(Math.max(e.clientX - r.left, r.right - e.clientX), Math.max(e.clientY - r.top, r.bottom - e.clientY)) * 2;
		const dot = document.createElement('span');
		dot.className = 'm-ripple';
		dot.setAttribute('aria-hidden', 'true');
		dot.style.width = dot.style.height = `${size}px`;
		dot.style.left = `${e.clientX - r.left - size / 2}px`;
		dot.style.top = `${e.clientY - r.top - size / 2}px`;
		dot.addEventListener('animationend', () => dot.remove(), { once: true });
		host.appendChild(dot);
	};
	document.addEventListener('pointerdown', onDown, true);
	return () => document.removeEventListener('pointerdown', onDown, true);
}

type VTDocument = Document & { startViewTransition?: (cb: () => unknown) => { finished: Promise<void> } };

/** Runs `update` inside a view transition when the browser supports one and motion is allowed. */
export function viewTransition(update: () => unknown, kind?: string) {
	const doc = document as VTDocument;
	if (!doc.startViewTransition || reducedMotion()) {
		void update();
		return;
	}
	const root = document.documentElement;
	if (kind) root.dataset.vt = kind;
	doc.startViewTransition(update).finished.finally(() => {
		if (kind && root.dataset.vt === kind) delete root.dataset.vt;
	});
}

/** [M44] Theme change revealed as a circle growing from (x, y). */
export function themeTransition(update: () => void, x?: number, y?: number) {
	const root = document.documentElement;
	if (x !== undefined && y !== undefined) {
		root.style.setProperty('--vt-x', `${x}px`);
		root.style.setProperty('--vt-y', `${y}px`);
	}
	viewTransition(update, 'theme');
}
