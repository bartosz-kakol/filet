<script lang="ts">
	import { ChevronRight, Download, Eye, Folder, Lock } from '@lucide/svelte';
	import { createVirtualizer } from '@tanstack/svelte-virtual';
	import { SvelteSet } from 'svelte/reactivity';
	import { untrack } from 'svelte';
	import type { Source } from '$lib/api/source';
	import type { ArchiveListing, Entry } from '$lib/api/types';
	import { formatBytes, formatCount, formatDate } from '$lib/files/format';
	import { NATIVE_IMAGE, ext, kindOf } from '$lib/files/kinds';
	import { buildTree, flatten, type TreeNode } from '$lib/preview/archiveTree';
	import type { ViewerControls } from '$lib/preview/controls.svelte';
	import { NATIVE_CONTAINERS } from '$lib/preview/media';
	import KindIcon from '../KindIcon.svelte';
	import Modal from '../Modal.svelte';
	import CodeViewer from './CodeViewer.svelte';
	import { ViewerControls as Controls } from '$lib/preview/controls.svelte';

	let { src, path, entry, controls }: { src: Source; path: string; entry: Entry; controls: ViewerControls } = $props();

	const INLINE_MAX = 256 * 1024 * 1024;
	let listing = $state<ArchiveListing | null>(null);
	let error = $state<string | null>(null);
	let filter = $state('');
	const expanded = new SvelteSet<string>();
	let scrollEl: HTMLDivElement | null = $state(null);

	$effect(() => {
		controls.actions = [];
		fetch(src.archiveList(path), { credentials: 'same-origin' })
			.then(async (r) => {
				if (!r.ok) {
					const b = await r.json().catch(() => null);
					throw new Error(b?.error?.message ?? `HTTP ${r.status}`);
				}
				return r.json();
			})
			.then((l: ArchiveListing) => {
				listing = l;
				controls.loaded = true;
			})
			.catch((e) => (error = String(e?.message ?? e)));
	});

	const tree = $derived(listing ? buildTree(listing.entries) : null);
	const rows = $derived.by(() => {
		if (!tree || !listing) return [];
		const f = filter.trim().toLowerCase();
		if (f) {
			return listing.entries
				.filter((e) => !e.isDir && e.path.toLowerCase().includes(f))
				.map((e) => ({ node: { name: e.path, path: e.path, isDir: false, entry: e, children: [] } as TreeNode, depth: 0 }));
		}
		return flatten(tree, expanded);
	});

	const virtual = createVirtualizer<HTMLDivElement, HTMLDivElement>({ count: 0, getScrollElement: () => scrollEl, estimateSize: () => 36, overscan: 15 });
	$effect(() => {
		const opts = { count: rows.length, getScrollElement: () => scrollEl };
		untrack(() => {
			$virtual.setOptions(opts);
			$virtual.measure();
		});
	});

	function previewable(n: TreeNode): boolean {
		const e = n.entry;
		if (!e || e.encrypted || e.size > INLINE_MAX) return false;
		const k = kindOf(n.name);
		if (k === 'image') return NATIVE_IMAGE.has(ext(n.name));
		if (k === 'text' || k === 'markdown' || k === 'csv') return e.size <= 2 * 1024 * 1024;
		if (k === 'video' || k === 'audio') return !!NATIVE_CONTAINERS[ext(n.name)];
		return false;
	}

	let previewNode = $state<TreeNode | null>(null);
	let previewOpen = $state(false);
	let previewText = $state<string | null>(null);
	const innerControls = new Controls();

	async function openPreview(n: TreeNode) {
		previewNode = n;
		previewText = null;
		previewOpen = true;
		const k = kindOf(n.name);
		if (k === 'text' || k === 'markdown' || k === 'csv') {
			const r = await fetch(src.archiveEntry(path, n.path), { credentials: 'same-origin' });
			previewText = r.ok ? await r.text() : `Could not load (${r.status})`;
		}
	}
</script>

{#if error}
	<div class="flex h-full items-center justify-center p-6 text-center text-sm text-zinc-400">{error}</div>
{:else if !listing}
	<div class="flex h-full items-center justify-center text-sm text-zinc-400">Reading archive…</div>
{:else}
	<div class="flex h-full flex-col">
		<div class="flex flex-wrap items-center gap-3 border-b border-white/10 px-3 py-2">
			<input class="input max-w-xs bg-zinc-900" placeholder="Filter by path" bind:value={filter} aria-label="Filter archive entries" />
			<span class="text-sm text-zinc-400">
				{formatCount(listing.entryCount)} files, {formatBytes(listing.totalSize)} uncompressed{listing.truncated ? ' (list truncated)' : ''}
			</span>
		</div>
		<div class="min-h-0 flex-1 overflow-auto" bind:this={scrollEl} data-testid="archive-tree">
			<div class="relative" style:height="{$virtual.getTotalSize()}px">
				{#each $virtual.getVirtualItems().filter((v) => v.index < rows.length) as v (v.key)}
					{@const r = rows[v.index]}
					{@const n = r.node}
					<div class="absolute inset-x-0 flex h-9 items-center gap-2 pr-2 text-sm hover:bg-white/5" style:top="{v.start}px" style:padding-left="{12 + r.depth * 16}px">
						{#if n.isDir}
							<button
								class="flex min-w-0 flex-1 items-center gap-1.5 text-left"
								onclick={() => (expanded.has(n.path) ? expanded.delete(n.path) : expanded.add(n.path))}
								aria-expanded={expanded.has(n.path)}
							>
								<ChevronRight class="size-4 shrink-0 text-zinc-500 transition-transform {expanded.has(n.path) ? 'rotate-90' : ''}" />
								<Folder class="size-4 shrink-0 text-blue-400" /><span class="truncate">{n.name}</span>
							</button>
						{:else}
							<span class="w-4 shrink-0"></span>
							<KindIcon kind={kindOf(n.name)} class="size-4" />
							<span class="min-w-0 flex-1 truncate" title={n.path}>{n.name}</span>
							{#if n.entry?.encrypted}<Lock class="size-3.5 text-zinc-500" aria-label="Encrypted" />{/if}
							<span class="w-20 text-right text-xs text-zinc-400 tabular-nums max-md:hidden">{formatBytes(n.entry?.size)}</span>
							{#if n.entry?.compressedSize !== undefined}
								<span class="w-20 text-right text-xs text-zinc-500 tabular-nums max-md:hidden">{formatBytes(n.entry.compressedSize)}</span>
							{/if}
							<span class="w-24 text-xs text-zinc-500 max-md:hidden">{formatDate(n.entry?.mtime)}</span>
							{#if previewable(n)}
								<button class="icon-btn size-7 text-zinc-100 hover:bg-white/10" aria-label="Preview {n.name}" onclick={() => openPreview(n)}><Eye class="size-4" /></button>
							{/if}
							{#if !n.entry?.encrypted}
								<a class="icon-btn size-7 text-zinc-100 hover:bg-white/10" aria-label="Download {n.name}" href={src.archiveEntry(path, n.path, true)} download><Download class="size-4" /></a>
							{/if}
						{/if}
					</div>
				{/each}
			</div>
		</div>
	</div>
{/if}

{#if previewNode}
	{@const k = kindOf(previewNode.name)}
	{@const url = src.archiveEntry(path, previewNode.path)}
	<Modal bind:open={previewOpen} title={previewNode.name} wide onClose={() => (previewNode = null)}>
		<div class="flex max-h-[70vh] min-h-48 items-center justify-center overflow-auto">
			{#if k === 'image'}
				<img src={url} alt={previewNode.name} class="max-h-[70vh] max-w-full object-contain" />
			{:else if k === 'video'}
				<!-- svelte-ignore a11y_media_has_caption -->
				<video src={url} controls class="max-h-[70vh] max-w-full"></video>
			{:else if k === 'audio'}
				<audio src={url} controls></audio>
			{:else if previewText === null}
				<p class="text-sm text-muted">Loading…</p>
			{:else}
				<div class="h-[60vh] w-full">
					<CodeViewer entry={{ ...entry, name: previewNode.name }} controls={innerControls} text={previewText} manageControls={false} />
				</div>
			{/if}
		</div>
	</Modal>
{/if}
