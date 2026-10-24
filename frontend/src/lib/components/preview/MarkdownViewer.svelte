<script lang="ts">
	import { BookOpen, Code } from '@lucide/svelte';
	import type { Source } from '$lib/api/source';
	import type { Entry } from '$lib/api/types';
	import { baseName, parentPath } from '$lib/files/path';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import type { ViewerControls } from '$lib/preview/controls.svelte';
	import { renderMarkdown } from '$lib/preview/markdown';
	import { fetchText } from '$lib/preview/prefetch';
	import { appTheme } from '$lib/preview/theme.svelte';
	import CodeViewer from './CodeViewer.svelte';

	let {
		src,
		path,
		dir,
		entry,
		controls,
		hrefFor
	}: {
		src: Source;
		path: string;
		dir: string;
		entry: Entry;
		controls: ViewerControls;
		hrefFor: (dir: string, query?: Record<string, string>) => string;
	} = $props();

	let text = $state<string | null>(null);
	let error = $state<string | null>(null);

	$effect(() => {
		fetchText(src.raw(path))
			.then((t) => {
				text = t;
				controls.loaded = true;
			})
			.catch((e) => (error = String(e?.message ?? e)));
	});

	const html = $derived(
		text === null
			? ''
			: renderMarkdown(text, {
					dir,
					rawUrl: (p) => src.raw(p),
					previewHref: (p) => hrefFor(parentPath(p), { preview: baseName(p) }),
					browseHref: (p) => hrefFor(p)
				})
	);

	$effect(() => {
		if (prefs.markdownMode === 'rendered') {
			controls.actions = [{ id: 'raw', label: 'Raw', icon: Code, onclick: () => (prefs.markdownMode = 'raw') }];
			controls.onKey = null;
		}
	});
</script>

{#if error}
	<div class="flex h-full items-center justify-center text-sm text-zinc-400">Could not load: {error}</div>
{:else if text === null}
	<div class="flex h-full items-center justify-center text-sm text-zinc-400">Loading…</div>
{:else if prefs.markdownMode === 'raw'}
	<div class="flex h-full flex-col">
		<div class="flex justify-end border-b border-white/10 px-2 py-1">
			<button class="btn btn-ghost h-7 text-zinc-100 hover:bg-white/10" onclick={() => (prefs.markdownMode = 'rendered')}><BookOpen class="size-4" />Rendered</button>
		</div>
		<div class="min-h-0 flex-1"><CodeViewer {entry} {controls} {text} filename="x.md" /></div>
	</div>
{:else}
	<div class="h-full overflow-auto {appTheme.dark ? 'dark bg-zinc-950' : 'bg-white text-zinc-900'}">
		<!-- Sanitised with DOMPurify in renderMarkdown. -->
		<article class="md-prose mx-auto px-6 py-8" data-testid="markdown">{@html html}</article>
	</div>
{/if}
