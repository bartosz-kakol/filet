<script lang="ts">
	import { ArrowDownToLine, ArrowUpToLine, RefreshCw, TextWrap } from '@lucide/svelte';
	import { createVirtualizer } from '@tanstack/svelte-virtual';
	import { untrack } from 'svelte';
	import type { Source } from '$lib/api/source';
	import type { Entry, TextIndex } from '$lib/api/types';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import type { ViewerControls } from '$lib/preview/controls.svelte';

	let { src, path, entry, controls }: { src: Source; path: string; entry: Entry; controls: ViewerControls } = $props();

	const MAX_LINE = 10_000;
	const MAX_BLOCKS = 64;
	let index = $state<TextIndex | null>(null);
	let error = $state<string | null>(null);
	let version = $state(0);
	const blocks = new Map<number, string[]>();
	const pending = new Set<number>();
	let scrollEl: HTMLDivElement | null = $state(null);

	async function loadIndex() {
		error = null;
		try {
			const r = await fetch(src.textIndex(path), { credentials: 'same-origin' });
			if (!r.ok) {
				const b = await r.json().catch(() => null);
				error = b?.error?.code === 'index_timeout' ? 'File too large to index; download it instead' : (b?.error?.message ?? `HTTP ${r.status}`);
				return;
			}
			blocks.clear();
			index = await r.json();
			version++;
			controls.loaded = true;
		} catch (e) {
			error = String(e);
		}
	}

	$effect(() => {
		void loadIndex();
	});

	function request(b: number) {
		if (!index || pending.has(b) || blocks.has(b)) return;
		pending.add(b);
		const start = index.offsets[b];
		const end = b + 1 < index.offsets.length ? index.offsets[b + 1] - 1 : null;
		fetch(src.raw(path), { headers: { Range: `bytes=${start}-${end ?? ''}` }, credentials: 'same-origin' })
			.then((r) => r.arrayBuffer())
			.then((buf) => {
				const text = new TextDecoder('utf-8').decode(buf);
				const lines = text.split('\n').map((l) => (l.endsWith('\r') ? l.slice(0, -1) : l));
				blocks.set(b, lines);
				if (blocks.size > MAX_BLOCKS) blocks.delete(blocks.keys().next().value!);
				version++;
			})
			.finally(() => pending.delete(b));
	}

	function line(i: number): string | null {
		void version;
		if (!index) return null;
		const b = Math.floor(i / index.step);
		const l = blocks.get(b);
		if (!l) {
			request(b);
			return null;
		}
		return l[i - b * index.step] ?? '';
	}

	function tint(l: string): string {
		if (/\b(FATAL|ERROR|ERR)\b/.test(l)) return 'text-red-400';
		if (/\bWARN(ING)?\b/.test(l)) return 'text-amber-300';
		if (/\bINFO\b/.test(l)) return 'text-sky-300';
		if (/\b(DEBUG|TRACE)\b/.test(l)) return 'text-zinc-500';
		return '';
	}

	const virtual = createVirtualizer<HTMLDivElement, HTMLDivElement>({
		count: 0,
		getScrollElement: () => scrollEl,
		estimateSize: () => 20,
		overscan: 30
	});
	$effect(() => {
		const opts = { count: index?.lineCount ?? 0, getScrollElement: () => scrollEl, estimateSize: () => 20 };
		void prefs.softWrap;
		untrack(() => {
			$virtual.setOptions(opts);
			$virtual.measure();
		});
	});

	const gutter = $derived(String(index?.lineCount ?? 0).length);

	$effect(() => {
		controls.actions = [
			{ id: 'top', label: 'Jump to start', icon: ArrowUpToLine, onclick: () => $virtual.scrollToIndex(0) },
			{ id: 'end', label: 'Jump to end', icon: ArrowDownToLine, onclick: () => index && $virtual.scrollToIndex(index.lineCount - 1, { align: 'end' }) },
			{ id: 'reload', label: 'Reload', icon: RefreshCw, onclick: () => void loadIndex() },
			{ id: 'wrap', label: 'Soft wrap', icon: TextWrap, active: prefs.softWrap, onclick: () => (prefs.softWrap = !prefs.softWrap) }
		];
	});
</script>

{#if error}
	<div class="flex h-full items-center justify-center p-6 text-center text-sm text-zinc-400">{error}</div>
{:else if !index}
	<div class="flex h-full items-center justify-center text-sm text-zinc-400">Indexing {entry.name}…</div>
{:else}
	<div class="h-full overflow-auto bg-zinc-950 font-mono text-[13px] leading-5" bind:this={scrollEl} data-testid="large-text">
		<div class="relative" style:height="{$virtual.getTotalSize()}px" style:min-width={prefs.softWrap ? undefined : 'max-content'}>
			{#each $virtual.getVirtualItems() as row (row.key)}
				{@const l = line(row.index)}
				<div
					class="absolute inset-x-0 flex"
					style:transform="translateY({row.start}px)"
					data-index={row.index}
					use:$virtual.measureElement
					data-testid="text-line"
				>
					<span class="sticky left-0 shrink-0 bg-zinc-950 pr-3 pl-2 text-right text-zinc-600 select-none" style:width="{gutter + 2}ch">{row.index + 1}</span>
					{#if l === null}
						<span class="text-zinc-700">…</span>
					{:else}
						<span class="pr-4 {prefs.softWrap ? 'break-all whitespace-pre-wrap' : 'whitespace-pre'} {tint(l)}"
							>{l.length > MAX_LINE ? l.slice(0, MAX_LINE) : l}{#if l.length > MAX_LINE}<span class="text-zinc-500"> … ({(l.length - MAX_LINE).toLocaleString()} more characters)</span>{/if}</span
						>
					{/if}
				</div>
			{/each}
		</div>
	</div>
{/if}
