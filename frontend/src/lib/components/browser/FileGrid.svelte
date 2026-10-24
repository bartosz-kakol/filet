<script lang="ts">
	import { Check, Ellipsis, Folder, LoaderCircle } from '@lucide/svelte';
	import { createVirtualizer } from '@tanstack/svelte-virtual';
	import { untrack } from 'svelte';
	import type { Entry } from '$lib/api/types';
	import { joinPath } from '$lib/files/path';
	import { viewport } from '$lib/stores/media.svelte';
	import KindIcon from '../KindIcon.svelte';
	import EntryBadges from './EntryBadges.svelte';
	import Thumb from './Thumb.svelte';
	import type { ItemCtl, Placeholder } from './types';

	let {
		entries,
		placeholders,
		ctl,
		scrollEl,
		layout = $bindable({ folderCols: 1, fileCols: 1, dirCount: 0 })
	}: {
		entries: Entry[];
		placeholders: Placeholder[];
		ctl: ItemCtl;
		scrollEl: HTMLElement | null;
		layout?: { folderCols: number; fileCols: number; dirCount: number };
	} = $props();

	const GAP = 8;
	const PAD = 12;
	let width = $state(800);

	type VRow = { t: 'header'; label: string } | { t: 'folders'; from: number; to: number } | { t: 'files'; from: number; to: number };

	const dirCount = $derived(entries.findIndex((e) => e.type !== 'dir') === -1 ? entries.length : entries.findIndex((e) => e.type !== 'dir'));
	const fileTotal = $derived(entries.length - dirCount + placeholders.length);
	const inner = $derived(Math.max(100, width - 2 * PAD));
	const cols = (min: number) => (viewport.mobile ? 2 : Math.max(1, Math.floor((inner + GAP) / (min + GAP))));
	const folderCols = $derived(cols(200));
	const fileCols = $derived(cols(180));
	const cardW = $derived((inner - GAP * (fileCols - 1)) / fileCols);
	const fileRowH = $derived(Math.round(40 + cardW * 0.75 + 2 + GAP));

	$effect(() => {
		layout = { folderCols, fileCols, dirCount };
	});

	const rows = $derived.by(() => {
		const out: VRow[] = [];
		if (dirCount) {
			out.push({ t: 'header', label: 'Folders' });
			for (let i = 0; i < dirCount; i += folderCols) out.push({ t: 'folders', from: i, to: Math.min(dirCount, i + folderCols) });
		}
		if (fileTotal) {
			out.push({ t: 'header', label: 'Files' });
			for (let i = 0; i < fileTotal; i += fileCols)
				out.push({ t: 'files', from: dirCount + i, to: Math.min(dirCount + fileTotal, dirCount + i + fileCols) });
		}
		return out;
	});

	const sizeOf = (r: VRow) => (r.t === 'header' ? 36 : r.t === 'folders' ? 48 + GAP : fileRowH);
	const virtual = createVirtualizer<HTMLElement, HTMLDivElement>({
		count: 0,
		getScrollElement: () => scrollEl,
		estimateSize: () => 56,
		overscan: 2
	});
	$effect(() => {
		const rs = rows;
		const h = fileRowH;
		const opts = { count: rs.length, getScrollElement: () => scrollEl, estimateSize: (i: number) => (rs[i].t === 'files' ? h : sizeOf(rs[i])) };
		untrack(() => {
			$virtual.setOptions(opts);
			$virtual.measure();
		});
	});

	export function scrollToIndex(index: number) {
		const r = rows.findIndex((row) => row.t !== 'header' && index >= row.from && index < row.to);
		if (r >= 0) $virtual.scrollToIndex(r, { align: 'auto' });
	}

	const range = (a: number, b: number) => Array.from({ length: b - a }, (_, k) => a + k);
</script>

<div class="relative" bind:clientWidth={width} style:height="{$virtual.getTotalSize()}px">
	{#each $virtual.getVirtualItems().filter((v) => v.index < rows.length) as vr (vr.key)}
		{@const r = rows[vr.index]}
		<div class="absolute inset-x-0 px-3" style:top="{vr.start}px" style:height="{sizeOf(r)}px">
			{#if r.t === 'header'}
				<h3 class="flex h-full items-end pb-2 text-xs font-medium tracking-wide text-muted uppercase">{r.label}</h3>
			{:else if r.t === 'folders'}
				<div class="grid gap-2" style:grid-template-columns="repeat({folderCols}, minmax(0, 1fr))">
					{#each range(r.from, r.to).filter((i) => i < entries.length) as i (entries[i].name)}
						{@const e = entries[i]}
						{@const sel = ctl.selected(e.name)}
						<!-- [M16] m-folder, [M18] m-enter, [M04] data-ripple, [M47] m-focus -->
						<div
							role="gridcell"
							aria-selected={sel}
							data-ripple
							style:--i={i}
							tabindex="-1"
							data-testid="entry"
							data-name={e.name}
							data-index={i}
							draggable={!ctl.isPublic && !viewport.coarse}
							class="m-folder m-enter group flex h-12 cursor-default items-center gap-2 rounded-lg border px-3 select-none {sel
								? 'border-accent bg-accent/15'
								: 'border-line hover:bg-surface'} {ctl.focused === i ? 'm-focus outline-2 outline-accent/60' : ''} data-[drop=true]:bg-accent/20 data-[drop=true]:outline-2 data-[drop=true]:outline-accent"
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
							{...ctl.drop(e)}
						>
							<Folder class="m-folder-icon size-5 shrink-0 text-accent" />
							<span class="min-w-0 flex-1 truncate text-sm" title={e.name}>{e.name}</span>
							<EntryBadges entry={e} />
						</div>
					{/each}
				</div>
			{:else}
				<div class="grid gap-2" style:grid-template-columns="repeat({fileCols}, minmax(0, 1fr))">
					{#each range(r.from, r.to).filter((i) => i < entries.length + placeholders.length) as i (i < entries.length ? entries[i].name : placeholders[i - entries.length].key)}
						{#if i < entries.length}
							{@const e = entries[i]}
							{@const sel = ctl.selected(e.name)}
							<!-- [M15] m-card, [M18] m-enter, [M04] data-ripple, [M47] m-focus -->
							<div
								role="gridcell"
								aria-selected={sel}
								data-ripple
								style:--i={i - dirCount}
								tabindex="-1"
								data-testid="entry"
								data-name={e.name}
								data-index={i}
								draggable={!ctl.isPublic && !viewport.coarse}
								class="m-card m-enter group relative flex cursor-default flex-col overflow-hidden rounded-lg border select-none {sel
									? 'border-accent ring-2 ring-accent/40'
									: 'border-line'} {ctl.focused === i ? 'm-focus outline-2 outline-accent/60' : ''}"
								onclick={(ev) => ctl.click(i, ev)}
								onkeydown={(ev) => ev.key === 'Enter' && ctl.dblclick(i)}
								ondblclick={() => ctl.dblclick(i)}
								oncontextmenu={(ev) => ctl.menu(i, ev)}
								onpointerdown={(ev) => ctl.pointerdown(i, ev)}
								onpointerup={() => ctl.pointerup()}
								onpointercancel={() => ctl.pointerup()}
								onpointerleave={() => ctl.pointerup()}
								ondragstart={(ev) => ctl.dragstart(i, ev)}
								onmouseenter={() => ctl.hover(i, true)}
								onmouseleave={() => ctl.hover(i, false)}
							>
								<div class="flex h-10 items-center gap-2 px-2 transition-colors {sel ? 'bg-accent/15' : 'bg-bg group-hover:bg-surface'}">
									{#if ctl.multi}
										<span class="m-check flex size-4 shrink-0 items-center justify-center rounded border transition-colors {sel ? 'border-accent bg-accent text-accent-fg' : 'border-line'}">
											{#if sel}<Check class="m-check size-3" />{/if}
										</span>
									{:else}
										<KindIcon kind={e.kind} class="size-4" />
									{/if}
									<span class="min-w-0 flex-1 truncate text-sm {e.badName ? 'text-muted italic' : ''}" title={e.name}>{e.name}</span>
									<EntryBadges entry={e} />
									<button
										class="icon-btn -mr-1 size-7 {viewport.coarse ? '' : 'opacity-0 group-hover:opacity-100 focus:opacity-100'}"
										aria-label="Actions for {e.name}"
										onclick={(ev) => {
											ev.stopPropagation();
											ctl.menu(i, ev);
										}}><Ellipsis class="size-4" /></button
									>
								</div>
								<div class="aspect-[4/3] border-t border-line">
									<Thumb
										kind={e.type === 'symlink' ? 'other' : e.kind}
										src={e.thumbable ? ctl.src.thumb(joinPath(ctl.dir, e.name), e) : null}
									/>
								</div>
							</div>
						{:else}
							{@const p = placeholders[i - entries.length]}
							<!-- [M31] -->
							<div class="m-shimmer flex flex-col overflow-hidden rounded-lg border border-line opacity-50 select-none" data-testid="upload-placeholder" aria-disabled="true">
								<div class="flex h-10 items-center gap-2 px-2">
									<LoaderCircle class="size-4 shrink-0 text-muted {p.leftover ? '' : 'animate-spin'}" />
									<span class="truncate text-sm">{p.name}</span>
								</div>
								<div class="flex aspect-[4/3] items-center justify-center border-t border-line bg-surface-2 text-xs text-muted">
									{#if p.leftover}<a href="/leftovers" class="underline">Interrupted</a>{:else}Uploading {Math.floor(p.progress * 100)} %{/if}
								</div>
							</div>
						{/if}
					{/each}
				</div>
			{/if}
		</div>
	{/each}
</div>
