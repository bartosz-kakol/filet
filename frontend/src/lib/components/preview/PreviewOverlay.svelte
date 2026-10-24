<script lang="ts">
	import { ArrowLeft, ChevronLeft, ChevronRight, Download, ExternalLink, Info, Link2, Tv } from '@lucide/svelte';
	import { onDestroy, onMount } from 'svelte';
	import type { Source } from '$lib/api/source';
	import type { Entry, Place } from '$lib/api/types';
	import { downloadFile } from '$lib/files/actions';
	import { joinPath } from '$lib/files/path';
	import { mfade, mslide } from '$lib/motion';
	import { ViewerControls } from '$lib/preview/controls.svelte';
	import { Prefetcher } from '$lib/preview/prefetch';
	import { viewport } from '$lib/stores/media.svelte';
	import InfoPanel from './InfoPanel.svelte';
	import Viewer from './Viewer.svelte';

	let {
		src,
		place,
		dir,
		entry,
		sequence,
		onStep,
		onClose,
		onShare,
		onStream,
		hrefFor,
		closable = true
	}: {
		src: Source;
		place: Place | null;
		dir: string;
		entry: Entry;
		sequence: Entry[];
		onStep: (name: string) => void;
		onClose: () => void;
		onShare?: (e: Entry) => void;
		onStream?: (e: Entry) => void;
		hrefFor: (dir: string, query?: Record<string, string>) => string;
		closable?: boolean;
	} = $props();

	const controls = new ViewerControls();
	let prefetcher: Prefetcher | null = null;
	let infoOpen = $state(false);
	const index = $derived(sequence.findIndex((e) => e.name === entry.name));
	const prev = $derived(index > 0 ? sequence[index - 1] : undefined);
	const next = $derived(index >= 0 && index < sequence.length - 1 ? sequence[index + 1] : undefined);
	const path = $derived(joinPath(dir, entry.name));
	const isMedia = $derived(entry.kind === 'video' || entry.kind === 'audio');

	// [M33] Direction of the last step, so the next file slides in from that side.
	let stepDir = $state(0);
	let lastIndex = -1;
	$effect.pre(() => {
		if (lastIndex >= 0 && index >= 0 && index !== lastIndex) stepDir = index > lastIndex ? 1 : -1;
		lastIndex = index;
	});

	// Prefetch neighbours once the current item has loaded.
	$effect(() => {
		if (!controls.loaded) return;
		prefetcher ??= new Prefetcher(src);
		prefetcher.update(dir, entry, prev, next);
	});
	onDestroy(() => prefetcher?.dispose());

	function step(e: Entry | undefined) {
		if (e) onStep(e.name);
	}

	function onKey(e: KeyboardEvent) {
		const t = e.target as HTMLElement;
		if (t.closest('input, textarea, select, [contenteditable], .cm-editor')) {
			if (e.key === 'Escape') (t as HTMLElement).blur();
			return;
		}
		if (document.querySelector('[role=dialog][data-state=open]:not([data-preview])')) return;
		if (controls.onKey?.(e)) {
			e.preventDefault();
			return;
		}
		switch (e.key) {
			case 'ArrowLeft':
				e.preventDefault();
				step(prev);
				break;
			case 'ArrowRight':
				e.preventDefault();
				step(next);
				break;
			case 'Escape':
				if (!closable) break;
				e.preventDefault();
				onClose();
				break;
			case 'i':
				infoOpen = !infoOpen;
				break;
		}
	}

	// Swipe navigation (touch): ≥ 50 px, more horizontal than vertical.
	let sx = 0;
	let sy = 0;
	let tracking = false;
	function touchStart(e: TouchEvent) {
		if (e.touches.length !== 1 || !controls.canSwipe) return;
		tracking = true;
		sx = e.touches[0].clientX;
		sy = e.touches[0].clientY;
	}
	function touchEnd(e: TouchEvent) {
		if (!tracking) return;
		tracking = false;
		const t = e.changedTouches[0];
		const dx = t.clientX - sx;
		const dy = t.clientY - sy;
		if (Math.abs(dx) >= 50 && Math.abs(dx) > Math.abs(dy) && controls.canSwipe) step(dx < 0 ? next : prev);
	}

	let main: HTMLElement | undefined = $state();
	onMount(() => {
		main?.focus();
		const prevOverflow = document.body.style.overflow;
		document.body.style.overflow = 'hidden';
		return () => (document.body.style.overflow = prevOverflow);
	});
</script>

<svelte:window onkeydown={onKey} />

<!-- [M32] m-preview zooms in; fades out on close -->
<div
	class="m-preview dark fixed inset-0 z-40 flex flex-col bg-zinc-950 text-zinc-100"
	out:mfade|global={{ duration: 160 }}
	role="dialog"
	aria-modal="true"
	aria-label="Preview of {entry.name}"
	data-preview="true"
	data-testid="preview"
>
	<header class="flex h-12 shrink-0 items-center gap-1 border-b border-white/10 px-1 md:px-2">
		{#if closable}
			<button class="icon-btn text-zinc-100 hover:bg-white/10" aria-label="Close preview" title="Close (Esc)" onclick={onClose}><ArrowLeft class="size-5" /></button>
		{/if}
		<div class="min-w-0 flex-1 truncate px-1 text-sm font-medium" title={entry.name}>{entry.name}</div>
		{#if sequence.length > 1 && index >= 0}
			<span class="px-2 text-xs text-zinc-400 tabular-nums max-md:hidden" data-testid="preview-counter">{index + 1} / {sequence.length}</span>
		{/if}
		<div class="flex items-center overflow-x-auto [scrollbar-width:none]">
			{#each controls.actions as a (a.id)}
				<button
					class="icon-btn text-zinc-100 hover:bg-white/10 {a.active ? 'bg-white/15' : ''}"
					aria-label={a.label}
					title={a.title ?? a.label}
					aria-pressed={a.active}
					disabled={a.disabled}
					onclick={a.onclick}><a.icon class="size-4" /></button
				>
			{/each}
			<button class="icon-btn text-zinc-100 hover:bg-white/10 {infoOpen ? 'bg-white/15' : ''}" aria-label="Info" title="Info (i)" onclick={() => (infoOpen = !infoOpen)}><Info class="size-4" /></button>
			<button class="icon-btn text-zinc-100 hover:bg-white/10" aria-label="Download" title="Download" onclick={() => downloadFile(src, path)}><Download class="size-4" /></button>
			{#if !src.isPublic && onShare}
				<button class="icon-btn text-zinc-100 hover:bg-white/10 max-md:hidden" aria-label="Share link" title="Share link" onclick={() => onShare?.(entry)}><Link2 class="size-4" /></button>
			{/if}
			{#if !src.isPublic && isMedia && onStream}
				<button class="icon-btn text-zinc-100 hover:bg-white/10" aria-label="Open in external player" title="Open in external player" onclick={() => onStream?.(entry)}><Tv class="size-4" /></button>
			{/if}
			<a class="icon-btn text-zinc-100 hover:bg-white/10 max-md:hidden" aria-label="Open raw in new tab" title="Open raw in new tab" href={src.raw(path)} target="_blank" rel="noopener"><ExternalLink class="size-4" /></a>
		</div>
	</header>
	<div class="flex min-h-0 flex-1 max-md:flex-col">
		<div
			class="group relative min-h-0 min-w-0 flex-1 outline-none"
			role="region"
			aria-label="Viewer"
			tabindex="-1"
			bind:this={main}
			ontouchstart={touchStart}
			ontouchend={touchEnd}
		>
			{#key entry.name}
				<div class="m-step h-full w-full" data-dir={stepDir}><!-- [M33] -->
					<Viewer {src} {dir} {entry} {controls} {place} {hrefFor} />
				</div>
			{/key}
			{#if !viewport.coarse}
				{#if prev}
					<!-- [M34] m-nudge -->
					<button
						class="m-nudge absolute top-1/2 left-2 z-10 flex size-12 -translate-y-1/2 items-center justify-center rounded-full bg-black/40 text-white opacity-0 transition-opacity group-hover:opacity-100 hover:bg-black/60 focus:opacity-100"
						data-dir="prev"
						aria-label="Previous file"
						onclick={() => step(prev)}><ChevronLeft class="size-7" /></button
					>
				{/if}
				{#if next}
					<button
						class="m-nudge absolute top-1/2 right-2 z-10 flex size-12 -translate-y-1/2 items-center justify-center rounded-full bg-black/40 text-white opacity-0 transition-opacity group-hover:opacity-100 hover:bg-black/60 focus:opacity-100"
						data-dir="next"
						aria-label="Next file"
						onclick={() => step(next)}><ChevronRight class="size-7" /></button
					>
				{/if}
			{/if}
		</div>
		{#if infoOpen}
			<!-- [M35] info panel slides open sideways (desktop) or upwards (mobile) -->
			<aside transition:mslide={{ axis: viewport.mobile ? 'y' : 'x', duration: 260 }} class="shrink-0 overflow-auto border-white/10 bg-zinc-900 md:w-[360px] md:border-l max-md:max-h-[50vh] max-md:border-t" aria-label="File information">
				<InfoPanel {src} {dir} {entry} dims={controls.dims} />
			</aside>
		{/if}
	</div>
</div>
