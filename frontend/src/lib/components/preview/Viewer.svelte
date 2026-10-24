<script lang="ts">
	import type { Source } from '$lib/api/source';
	import type { Entry, Place } from '$lib/api/types';
	import { ext } from '$lib/files/kinds';
	import { joinPath } from '$lib/files/path';
	import type { ViewerControls } from '$lib/preview/controls.svelte';
	import { SMALL_TEXT_LIMIT, looksLikeText } from '$lib/preview/sniff';
	import { places } from '$lib/stores/places.svelte';
	import ImageViewer from './ImageViewer.svelte';
	import LargeTextViewer from './LargeTextViewer.svelte';
	import MediaPlayer from './MediaPlayer.svelte';
	import NoPreview from './NoPreview.svelte';
	import PdfViewer from './PdfViewer.svelte';

	let {
		src,
		dir,
		entry,
		controls,
		place,
		hrefFor
	}: {
		src: Source;
		dir: string;
		entry: Entry;
		controls: ViewerControls;
		place: Place | null;
		hrefFor: (dir: string, query?: Record<string, string>) => string;
	} = $props();

	type Mode = 'pending' | 'image' | 'media' | 'code' | 'markdown' | 'large' | 'csv' | 'pdf' | 'archive' | 'font' | 'none';
	const path = $derived(joinPath(dir, entry.name));
	const CSV_LIMIT = 20 * 1024 * 1024;
	// ".ts" is an MPEG transport stream or TypeScript: sniff to decide.
	const ambiguous = $derived(ext(entry.name) === 'ts');
	const needsSniff = $derived(entry.kind === 'other' || ambiguous);
	let sniffed = $state<'text' | 'binary' | null>(null);

	const mode: Mode = $derived.by(() => {
		const small = entry.size <= SMALL_TEXT_LIMIT;
		if (ambiguous) {
			if (sniffed === null) return 'pending';
			if (sniffed === 'text') return small ? 'code' : 'large';
		}
		switch (entry.kind) {
			case 'image':
				return 'image';
			case 'image-render':
				return places.settings?.capabilities.renditions === false ? 'none' : 'image';
			case 'video':
			case 'audio':
				return 'media';
			case 'markdown':
				return small ? 'markdown' : 'large';
			case 'text':
				return small ? 'code' : 'large';
			case 'csv':
				return entry.size <= CSV_LIMIT ? 'csv' : 'large';
			case 'pdf':
				return 'pdf';
			case 'archive':
				return 'archive';
			case 'font':
				return entry.size <= CSV_LIMIT ? 'font' : 'none';
		}
		if (sniffed === null) return 'pending';
		if (sniffed === 'binary') return 'none';
		return small ? 'code' : 'large';
	});

	$effect(() => {
		if (!needsSniff || sniffed !== null) return;
		if (entry.size === 0) {
			sniffed = 'text';
			return;
		}
		const ac = new AbortController();
		fetch(src.raw(path), { headers: { Range: 'bytes=0-8191' }, credentials: 'same-origin', signal: ac.signal })
			.then((r) => (r.ok ? r.arrayBuffer() : Promise.reject(new Error(String(r.status)))))
			.then((b) => (sniffed = looksLikeText(new Uint8Array(b)) ? 'text' : 'binary'))
			.catch(() => (sniffed = 'binary'));
		return () => ac.abort();
	});

	$effect(() => {
		if (mode === 'none' || mode === 'pdf') controls.loaded = true;
	});
</script>

{#if mode === 'pending'}
	<div class="flex h-full items-center justify-center text-sm text-zinc-400">Loading…</div>
{:else if mode === 'image'}
	<ImageViewer {src} {path} {entry} {controls} />
{:else if mode === 'media'}
	<MediaPlayer {src} {path} {entry} {controls} {place} />
{:else if mode === 'code'}
	{#await import('./CodeViewer.svelte') then m}
		<m.default {src} {path} {entry} {controls} />
	{/await}
{:else if mode === 'markdown'}
	{#await import('./MarkdownViewer.svelte') then m}
		<m.default {src} {path} {dir} {entry} {controls} {hrefFor} />
	{/await}
{:else if mode === 'large'}
	<LargeTextViewer {src} {path} {entry} {controls} />
{:else if mode === 'csv'}
	{#await import('./CsvViewer.svelte') then m}
		<m.default {src} {path} {entry} {controls} />
	{/await}
{:else if mode === 'pdf'}
	<PdfViewer {src} {path} {entry} />
{:else if mode === 'archive'}
	{#await import('./ArchiveViewer.svelte') then m}
		<m.default {src} {path} {entry} {controls} />
	{/await}
{:else if mode === 'font'}
	{#await import('./FontViewer.svelte') then m}
		<m.default {src} {path} {entry} {controls} />
	{/await}
{:else}
	<NoPreview {src} {path} {entry} />
{/if}
