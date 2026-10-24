<script lang="ts">
	import { Code, Table } from '@lucide/svelte';
	import { createVirtualizer } from '@tanstack/svelte-virtual';
	import { untrack } from 'svelte';
	import type { Source } from '$lib/api/source';
	import type { Entry } from '$lib/api/types';
	import { ext } from '$lib/files/kinds';
	import type { ViewerControls } from '$lib/preview/controls.svelte';
	import { parseCSV } from '$lib/preview/csv';
	import { fetchText } from '$lib/preview/prefetch';
	import CodeViewer from './CodeViewer.svelte';

	let { src, path, entry, controls }: { src: Source; path: string; entry: Entry; controls: ViewerControls } = $props();

	const COL_W = 180;
	let text = $state<string | null>(null);
	let error = $state<string | null>(null);
	let header = $state(true);
	let raw = $state(false);
	let scrollEl: HTMLDivElement | null = $state(null);

	$effect(() => {
		fetchText(src.raw(path))
			.then((t) => {
				text = t;
				controls.loaded = true;
			})
			.catch((e) => (error = String(e?.message ?? e)));
	});

	const parsed = $derived(text === null ? null : parseCSV(text, ext(entry.name) === 'tsv' ? '\t' : ','));
	const cols = $derived(parsed ? Math.max(0, ...parsed.rows.slice(0, 1000).map((r) => r.length)) : 0);
	const head = $derived(header && parsed?.rows.length ? parsed.rows[0] : null);
	const body = $derived(parsed ? (header ? parsed.rows.slice(1) : parsed.rows) : []);

	const rowsV = createVirtualizer<HTMLDivElement, HTMLDivElement>({ count: 0, getScrollElement: () => scrollEl, estimateSize: () => 28, overscan: 20 });
	const colsV = createVirtualizer<HTMLDivElement, HTMLDivElement>({ count: 0, getScrollElement: () => scrollEl, estimateSize: () => COL_W, horizontal: true, overscan: 4 });
	$effect(() => {
		const r = { count: body.length, getScrollElement: () => scrollEl, estimateSize: () => 28 };
		const c = { count: cols + 1, getScrollElement: () => scrollEl, estimateSize: (i: number) => (i === 0 ? 64 : COL_W), horizontal: true };
		untrack(() => {
			$rowsV.setOptions(r);
			$colsV.setOptions(c);
			$rowsV.measure();
			$colsV.measure();
		});
	});

	$effect(() => {
		controls.actions = [
			{ id: 'header', label: 'First row is header', icon: Table, active: header, onclick: () => (header = !header) },
			{ id: 'raw', label: 'Raw', icon: Code, active: raw, onclick: () => (raw = !raw) }
		];
	});
</script>

{#if error}
	<div class="flex h-full items-center justify-center text-sm text-zinc-400">Could not load: {error}</div>
{:else if text === null}
	<div class="flex h-full items-center justify-center text-sm text-zinc-400">Loading…</div>
{:else if raw}
	<CodeViewer {entry} {controls} {text} manageControls={false} />
{:else}
	<div class="h-full overflow-auto bg-zinc-950 text-sm" bind:this={scrollEl} data-testid="csv">
		<div class="relative" style:width="{$colsV.getTotalSize()}px" style:height="{$rowsV.getTotalSize() + 28}px">
			<div class="sticky top-0 z-10 h-7 bg-zinc-800 font-medium">
				{#each $colsV.getVirtualItems() as c (c.key)}
					<div class="absolute top-0 h-7 truncate border-r border-white/10 px-2 leading-7" style:left="{c.start}px" style:width="{c.size}px">
						{c.index === 0 ? '#' : (head?.[c.index - 1] ?? `Column ${c.index}`)}
					</div>
				{/each}
			</div>
			{#each $rowsV.getVirtualItems().filter((v) => v.index < body.length) as r (r.key)}
				{@const row = body[r.index]}
				{#each $colsV.getVirtualItems() as c (c.key)}
					<div
						class="absolute h-7 truncate border-r border-b border-white/5 px-2 leading-7 {c.index === 0 ? 'text-right text-zinc-500' : ''}"
						style:top="{r.start + 28}px"
						style:left="{c.start}px"
						style:width="{c.size}px"
						title={c.index === 0 ? undefined : row?.[c.index - 1]}
					>
						{c.index === 0 ? r.index + 1 : (row?.[c.index - 1] ?? '')}
					</div>
				{/each}
			{/each}
		</div>
	</div>
	{#if parsed?.truncated}
		<p class="absolute bottom-2 left-2 rounded bg-black/70 px-2 py-1 text-xs">Showing the first 100,000 rows</p>
	{/if}
{/if}
