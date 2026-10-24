<script lang="ts">
	import { Check, Ellipsis, LoaderCircle } from '@lucide/svelte';
	import { createVirtualizer } from '@tanstack/svelte-virtual';
	import { untrack } from 'svelte';
	import type { Entry } from '$lib/api/types';
	import { formatBytes, formatDate } from '$lib/files/format';
	import { ext } from '$lib/files/kinds';
	import type { Sort, SortKey } from '$lib/prefs/storage';
	import { viewport } from '$lib/stores/media.svelte';
	import KindIcon from '../KindIcon.svelte';
	import EntryBadges from './EntryBadges.svelte';
	import type { ItemCtl, Placeholder } from './types';

	let {
		entries,
		placeholders,
		ctl,
		scrollEl,
		sort,
		onSort
	}: {
		entries: Entry[];
		placeholders: Placeholder[];
		ctl: ItemCtl;
		scrollEl: HTMLElement | null;
		sort: Sort;
		onSort: (k: SortKey) => void;
	} = $props();

	const rowH = $derived(viewport.mobile ? 56 : 40);
	const total = $derived(entries.length + placeholders.length);
	const virtual = createVirtualizer<HTMLElement, HTMLDivElement>({
		count: 0,
		getScrollElement: () => scrollEl,
		estimateSize: () => 40,
		overscan: 12
	});
	$effect(() => {
		const opts = { count: total, getScrollElement: () => scrollEl, estimateSize: () => rowH };
		untrack(() => {
			$virtual.setOptions(opts);
			$virtual.measure();
		});
	});

	export function scrollToIndex(i: number) {
		$virtual.scrollToIndex(i, { align: 'auto' });
	}

	const headers: { key: SortKey; label: string; cls: string }[] = [
		{ key: 'name', label: 'Name', cls: 'flex-1' },
		{ key: 'size', label: 'Size', cls: 'w-24 text-right' },
		{ key: 'mtime', label: 'Modified', cls: 'w-32' },
		{ key: 'type', label: 'Type', cls: 'w-20' }
	];
</script>

{#if !viewport.mobile}
	<div class="sticky top-0 z-10 flex h-8 items-center gap-3 border-b border-line bg-bg px-3 text-xs text-muted select-none" role="row">
		<span class="w-5"></span>
		{#each headers as h (h.key)}
			<button class="{h.cls} truncate text-left transition-colors hover:text-fg {sort.key === h.key ? 'text-fg font-medium' : ''}" onclick={() => onSort(h.key)}>
				{h.label}{#if sort.key === h.key}{#key sort.dir}<span class="m-check inline-block whitespace-pre"> {sort.dir === 'asc' ? '↑' : '↓'}</span>{/key}{/if}<!-- [M12] -->
			</button>
		{/each}
		<span class="w-8"></span>
	</div>
{/if}
<div class="relative" style:height="{$virtual.getTotalSize()}px" role="grid" aria-rowcount={total}>
	{#each $virtual.getVirtualItems() as row (row.key)}
		{@const i = row.index}
		<div class="absolute inset-x-0" style:top="{row.start}px" style:height="{rowH}px">
			{#if i < entries.length}
				{@const e = entries[i]}
				{@const sel = ctl.selected(e.name)}
				<!-- [M14] m-row, [M18] m-enter, [M04] data-ripple, [M47] m-focus -->
				<div
					role="row"
					aria-selected={sel}
					data-ripple
					style:--i={i}
					tabindex="-1"
					data-testid="entry"
					data-name={e.name}
					data-index={i}
					draggable={!ctl.isPublic && !viewport.coarse}
					class="m-row m-enter group flex h-full cursor-default items-center gap-3 px-3 select-none {sel
						? 'bg-accent/15'
						: 'hover:bg-surface'} {ctl.focused === i ? 'm-focus outline-2 -outline-offset-2 outline-accent/60' : ''} data-[drop=true]:bg-accent/20 data-[drop=true]:outline-2 data-[drop=true]:outline-accent"
					onclick={(ev) => ctl.click(i, ev)}
					ondblclick={() => ctl.dblclick(i)}
					oncontextmenu={(ev) => ctl.menu(i, ev)}
					onpointerdown={(ev) => ctl.pointerdown(i, ev)}
					onpointerup={() => ctl.pointerup()}
					onpointercancel={() => ctl.pointerup()}
					onpointerleave={() => ctl.pointerup()}
					ondragstart={(ev) => ctl.dragstart(i, ev)}
					onmouseenter={() => ctl.hover(i, true)}
					onmouseleave={() => ctl.hover(i, false)}
					{...e.type === 'dir' ? ctl.drop(e) : {}}
				>
					<button
						class="flex size-5 shrink-0 items-center justify-center rounded border transition-[background-color,border-color,opacity] duration-150 {sel
							? 'border-accent bg-accent text-accent-fg'
							: 'border-line'} {ctl.multi ? '' : 'invisible'} {ctl.isPublic && !ctl.multi ? 'max-md:hidden' : ''}"
						aria-label="Select {e.name}"
						onclick={(ev) => {
							ev.stopPropagation();
							ctl.toggle(i);
						}}>{#if sel}<Check class="m-check size-3.5" />{/if}</button
					>
					<KindIcon kind={e.type === 'symlink' ? 'other' : e.kind} class="m-kind size-5" />
					<div class="flex min-w-0 flex-1 flex-col md:flex-row md:items-center md:gap-3">
						<div class="flex min-w-0 flex-1 items-center gap-1.5">
							<span
								class="truncate text-sm {e.badName ? 'text-muted italic' : ''} {e.symlink && e.symlink.status !== 'ok'
									? 'text-muted'
									: ''}"
								title={e.name}>{e.name}</span
							>
							<EntryBadges entry={e} />
						</div>
						{#if viewport.mobile}
							<span class="truncate text-xs text-muted">{e.type === 'dir' ? 'Folder' : `${formatBytes(e.size)} · ${formatDate(e.mtime)}`}</span>
						{:else}
							<span class="w-24 shrink-0 text-right text-sm text-muted tabular-nums">{e.type === 'dir' ? '—' : formatBytes(e.size)}</span>
							<span class="w-32 shrink-0 truncate text-sm text-muted">{formatDate(e.mtime)}</span>
							<span class="w-20 shrink-0 truncate text-sm text-muted uppercase">{e.type === 'dir' ? 'Folder' : ext(e.name) || '—'}</span>
						{/if}
					</div>
					<button
						class="icon-btn size-8 {viewport.coarse ? '' : 'opacity-0 group-hover:opacity-100 focus:opacity-100'}"
						aria-label="Actions for {e.name}"
						onclick={(ev) => {
							ev.stopPropagation();
							ctl.menu(i, ev);
						}}><Ellipsis class="size-4" /></button
					>
				</div>
			{:else if placeholders[i - entries.length]}
				{@const p = placeholders[i - entries.length]}
				<!-- [M31] -->
				<div class="m-shimmer flex h-full items-center gap-3 px-3 opacity-50 select-none" data-testid="upload-placeholder" aria-disabled="true">
					<span class="w-5"></span>
					<LoaderCircle class="size-5 shrink-0 text-muted {p.leftover ? '' : 'animate-spin'}" />
					<span class="flex-1 truncate text-sm">{p.name}</span>
					<span class="text-xs text-muted">
						{#if p.leftover}<a href="/leftovers" class="underline">Interrupted</a>{:else}Uploading {Math.floor(p.progress * 100)} %{/if}
					</span>
					<span class="w-8"></span>
				</div>
			{/if}
		</div>
	{/each}
</div>
