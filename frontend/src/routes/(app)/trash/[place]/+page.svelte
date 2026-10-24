<script lang="ts">
	import { ArchiveRestore, Check, Info, Trash2 } from '@lucide/svelte';
	import { onMount } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { errorMessage, get } from '$lib/api/client';
	import { events } from '$lib/api/events';
	import type { TrashItem } from '$lib/api/types';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import KindIcon from '$lib/components/KindIcon.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import { formatBytes, formatRelative } from '$lib/files/format';
	import { kindOf } from '$lib/files/kinds';
	import { jobs } from '$lib/jobs/jobs.svelte';
	import { places } from '$lib/stores/places.svelte';

	const placeId = $derived(page.params.place ?? '');
	const place = $derived(places.byId(placeId));
	let items = $state<TrashItem[]>([]);
	let error = $state<string | null>(null);
	let loaded = $state(false);
	const selection = new SvelteSet<string>();
	let purgeOpen = $state(false);
	let emptyOpen = $state(false);

	async function load() {
		if (!place || place.readonly) return;
		try {
			items = await get<TrashItem[]>(`/api/places/${encodeURIComponent(placeId)}/trash`);
			error = null;
		} catch (e) {
			error = errorMessage(e);
		} finally {
			loaded = true;
		}
	}

	$effect(() => {
		void placeId;
		selection.clear();
		loaded = false;
		void load();
	});

	onMount(() =>
		events.on('job.updated', (j: { type: string; status: string; spec: { place?: string } }) => {
			if ((j.type === 'trash' || j.type === 'restore' || j.type === 'purge') && j.spec.place === placeId && j.status === 'completed') void load();
		})
	);

	const ids = $derived([...selection]);
	async function restore() {
		await jobs.submit({ type: 'restore', place: placeId, ids });
		selection.clear();
	}
	async function purge() {
		await jobs.submit({ type: 'purge', place: placeId, ids });
		selection.clear();
	}
	async function emptyTrash() {
		await jobs.submit({ type: 'purge', place: placeId, all: true });
		selection.clear();
	}
	const writable = $derived(places.list.filter((p) => !p.readonly));
</script>

<svelte:head><title>Trash · Filet</title></svelte:head>

<div class="flex h-dvh flex-col">
	<PageHeader title="Trash" {placeId}>
		{#snippet actions()}
			<select class="input w-44" value={placeId} onchange={(e) => goto(`/trash/${encodeURIComponent((e.currentTarget as HTMLSelectElement).value)}`)} aria-label="Place">
				{#each writable as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
				{#if place?.readonly}<option value={place.id}>{place.name}</option>{/if}
			</select>
		{/snippet}
	</PageHeader>
	{#if !place}
		<p class="p-6 text-muted">Unknown place</p>
	{:else if place.readonly}
		<p class="p-6 text-muted">Read-only places have no trash</p>
	{:else}
		<div class="flex h-12 items-center gap-2 border-b border-line px-3 text-sm">
			<span class="text-muted">{selection.size ? `${selection.size} selected` : `${items.length} items`}</span>
			<span class="flex-1"></span>
			<button class="btn h-8" disabled={!selection.size} onclick={restore}><ArchiveRestore class="size-4" />Restore</button>
			<button class="btn h-8 text-danger" disabled={!selection.size} onclick={() => (purgeOpen = true)}><Trash2 class="size-4" />Delete permanently</button>
			<button class="btn btn-danger h-8" disabled={!items.length} onclick={() => (emptyOpen = true)}>Empty trash</button>
		</div>
		<div class="min-h-0 flex-1 overflow-auto">
			{#if error}
				<p class="p-6 text-danger">{error}</p>
			{:else if loaded && !items.length}
				<p class="p-6 text-center text-muted">Trash is empty</p>
			{/if}
			<table class="w-full text-sm">
				<tbody>
					{#each items as t (t.id)}
						{@const sel = selection.has(t.id)}
						<tr
							class="cursor-default border-b border-line select-none {sel ? 'bg-accent/15' : 'hover:bg-surface'}"
							onclick={() => (sel ? selection.delete(t.id) : selection.add(t.id))}
							data-testid="trash-row"
						>
							<td class="w-10 py-2 pl-3">
								<span class="flex size-5 items-center justify-center rounded border {sel ? 'border-accent bg-accent text-accent-fg' : 'border-line'}">{#if sel}<Check class="size-3.5" />{/if}</span>
							</td>
							<td class="py-2"><div class="flex items-center gap-2"><KindIcon kind={t.isDir ? 'dir' : kindOf(t.name)} /><span class="truncate">{t.name}</span></div></td>
							<td class="py-2 text-muted max-md:hidden">
								{#if t.originUnknown}
									<span class="inline-flex items-center gap-1" title="The original location could not be determined">Unknown <Info class="size-3.5" /></span>
								{:else}{t.originalPath}{/if}
							</td>
							<td class="py-2 text-muted">{formatRelative(t.deletedAt)}</td>
							<td class="py-2 pr-3 text-right text-muted tabular-nums">{t.size === null ? 'calculating…' : formatBytes(t.size)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	{/if}
</div>

<ConfirmDialog bind:open={purgeOpen} title="Delete permanently?" description="This cannot be undone" confirmLabel="Delete permanently" danger onConfirm={purge} />
<ConfirmDialog bind:open={emptyOpen} title="Empty trash?" description="Delete permanently? This cannot be undone" confirmLabel="Empty trash" danger onConfirm={emptyTrash} />
