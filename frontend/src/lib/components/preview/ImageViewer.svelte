<script lang="ts">
	import { Expand, RotateCcw, RotateCw, Scan, ZoomIn, ZoomOut } from '@lucide/svelte';
	import type { Source } from '$lib/api/source';
	import type { Entry } from '$lib/api/types';
	import type { ViewerControls } from '$lib/preview/controls.svelte';

	let { src, path, entry, controls }: { src: Source; path: string; entry: Entry; controls: ViewerControls } = $props();

	const MIN = 0.1;
	const MAX = 8;
	const fullUrl = $derived(entry.kind === 'image' ? src.raw(path) : src.render(path, entry));
	const thumbUrl = $derived(entry.thumbable ? src.thumb(path, entry) : null);

	let cw = $state(0);
	let ch = $state(0);
	let nw = $state(0);
	let nh = $state(0);
	let full = $state<string | null>(null);
	let failed = $state(false);
	let rot = $state(0);
	let fitMode = $state(true);
	let scale = $state(1);
	let tx = $state(0);
	let ty = $state(0);

	$effect(() => {
		const url = fullUrl;
		const img = new Image();
		img.decoding = 'async';
		img.src = url;
		let cancelled = false;
		img
			.decode()
			.then(() => {
				if (cancelled) return;
				nw = img.naturalWidth;
				nh = img.naturalHeight;
				controls.dims = { w: nw, h: nh };
				full = url;
				controls.loaded = true;
			})
			.catch(() => {
				if (cancelled) return;
				failed = true;
				controls.loaded = true;
			});
		return () => {
			cancelled = true;
			img.src = '';
		};
	});

	const turned = $derived(Math.abs(rot / 90) % 2 === 1);
	const rw = $derived(turned ? nh : nw);
	const rh = $derived(turned ? nw : nh);
	const fitScale = $derived(rw && rh && cw && ch ? Math.min(1, cw / rw, ch / rh) : 1);
	const s = $derived(fitMode ? fitScale : scale);

	$effect(() => {
		controls.canSwipe = fitMode;
	});

	function fit() {
		fitMode = true;
		tx = ty = 0;
	}

	function zoomTo(next: number, px = 0, py = 0) {
		const cur = s;
		next = Math.min(MAX, Math.max(MIN, next));
		const k = next / cur;
		tx = px - (px - (fitMode ? 0 : tx)) * k;
		ty = py - (py - (fitMode ? 0 : ty)) * k;
		scale = next;
		fitMode = false;
		if (Math.abs(next - fitScale) < 0.001) fit();
	}

	const rotate = (d: number) => {
		rot = (rot + d + 360) % 360;
		fit();
	};

	$effect(() => {
		controls.actions = [
			{ id: 'rl', label: 'Rotate left', icon: RotateCcw, onclick: () => rotate(-90), title: 'Rotate left (Shift+R)' },
			{ id: 'rr', label: 'Rotate right', icon: RotateCw, onclick: () => rotate(90), title: 'Rotate right (R)' },
			{ id: 'zo', label: 'Zoom out', icon: ZoomOut, onclick: () => zoomTo(s / 1.25), title: 'Zoom out (−)' },
			{ id: 'fit', label: 'Fit', icon: Scan, onclick: fit, active: fitMode, title: 'Fit (0)' },
			{ id: '100', label: '100 %', icon: Expand, onclick: () => zoomTo(1), active: !fitMode && Math.abs(scale - 1) < 0.001, title: '100 %' },
			{ id: 'zi', label: 'Zoom in', icon: ZoomIn, onclick: () => zoomTo(s * 1.25), title: 'Zoom in (+)' }
		];
		controls.onKey = (e) => {
			if (e.key === 'r') rotate(90);
			else if (e.key === 'R') rotate(-90);
			else if (e.key === '+' || e.key === '=') zoomTo(s * 1.25);
			else if (e.key === '-') zoomTo(s / 1.25);
			else if (e.key === '0') fit();
			else return false;
			return true;
		};
	});

	let box: HTMLDivElement | undefined = $state();
	const rel = (e: { clientX: number; clientY: number }) => {
		const r = box!.getBoundingClientRect();
		return [e.clientX - r.left - r.width / 2, e.clientY - r.top - r.height / 2] as const;
	};

	function wheel(e: WheelEvent) {
		if (!full) return;
		e.preventDefault();
		const [px, py] = rel(e);
		zoomTo(s * Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.002)), px, py);
	}

	function toggleZoom(e: { clientX: number; clientY: number }) {
		if (fitMode) {
			const [px, py] = rel(e);
			zoomTo(1, px, py);
		} else fit();
	}

	// Pointer handling: drag to pan, two pointers to pinch, double-tap.
	const pts = new Map<number, { x: number; y: number }>();
	let pinchDist = 0;
	let lastTap = 0;
	function down(e: PointerEvent) {
		pts.set(e.pointerId, { x: e.clientX, y: e.clientY });
		(e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
		if (pts.size === 2) {
			const [a, b] = [...pts.values()];
			pinchDist = Math.hypot(a.x - b.x, a.y - b.y);
		}
	}
	function move(e: PointerEvent) {
		const p = pts.get(e.pointerId);
		if (!p) return;
		if (pts.size === 2) {
			pts.set(e.pointerId, { x: e.clientX, y: e.clientY });
			const [a, b] = [...pts.values()];
			const d = Math.hypot(a.x - b.x, a.y - b.y);
			if (pinchDist > 0) {
				const [px, py] = rel({ clientX: (a.x + b.x) / 2, clientY: (a.y + b.y) / 2 });
				zoomTo(s * (d / pinchDist), px, py);
			}
			pinchDist = d;
			return;
		}
		if (!fitMode) {
			tx += e.clientX - p.x;
			ty += e.clientY - p.y;
		}
		pts.set(e.pointerId, { x: e.clientX, y: e.clientY });
	}
	function up(e: PointerEvent) {
		pts.delete(e.pointerId);
		if (pts.size < 2) pinchDist = 0;
		if (e.pointerType === 'touch') {
			const now = Date.now();
			if (now - lastTap < 300) {
				toggleZoom(e);
				lastTap = 0;
			} else lastTap = now;
		}
	}
</script>

<div
	bind:this={box}
	bind:clientWidth={cw}
	bind:clientHeight={ch}
	class="relative h-full w-full touch-none overflow-hidden select-none {fitMode ? '' : 'cursor-grab active:cursor-grabbing'}"
	role="img"
	aria-label={entry.name}
	onwheel={wheel}
	ondblclick={(e) => toggleZoom(e)}
	onpointerdown={down}
	onpointermove={move}
	onpointerup={up}
	onpointercancel={up}
>
	{#if full}
		<img
			src={full}
			alt={entry.name}
			draggable="false"
			data-testid="preview-image"
			class="absolute top-1/2 left-1/2 max-w-none"
			style:width="{nw}px"
			style:height="{nh}px"
			style:transform="translate(-50%, -50%) translate({fitMode ? 0 : tx}px, {fitMode ? 0 : ty}px) rotate({rot}deg) scale({s})"
		/>
	{:else if failed}
		<div class="flex h-full items-center justify-center text-sm text-zinc-400">Could not load this image</div>
	{:else if thumbUrl}
		<img src={thumbUrl} alt="" class="h-full w-full object-contain blur-[1px]" style:transform="rotate({rot}deg)" />
	{:else}
		<div class="flex h-full items-center justify-center text-sm text-zinc-400">Loading…</div>
	{/if}
	{#if !fitMode}
		<span class="pointer-events-none absolute bottom-3 left-1/2 -translate-x-1/2 rounded bg-black/60 px-2 py-0.5 text-xs">{Math.round(s * 100)} %</span>
	{/if}
</div>
