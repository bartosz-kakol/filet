<script lang="ts">
	import { ChevronDown, HardDrive } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import type { Place } from '$lib/api/types';
	import { formatBytes } from '$lib/files/format';
	import { dirHref } from '$lib/files/path';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import { places } from '$lib/stores/places.svelte';
	import Sheet from './Sheet.svelte';
	import SmartBadge from './SmartBadge.svelte';

	let { current }: { current: Place | undefined } = $props();
	let open = $state(false);
	let filter = $state('');

	$effect(() => {
		if (open) {
			filter = '';
			void places.refresh();
		}
	});

	const shown = $derived(
		filter ? places.list.filter((p) => p.name.toLowerCase().includes(filter.toLowerCase())) : places.list
	);

	function sameDrive(p: Place): string | null {
		if (!p.disk) return null;
		const other = places.list.find((o) => o.id !== p.id && o.disk?.fsid === p.disk!.fsid);
		return other?.name ?? null;
	}

	function pick(p: Place) {
		prefs.lastPlace = p.id;
		open = false;
		void goto(dirHref(p.id, '/'));
	}
</script>

<Sheet bind:open label="Choose place" triggerClass="flex h-9 min-w-0 items-center gap-1.5 rounded-md px-2 text-sm font-semibold hover:bg-surface-2">
	{#snippet trigger()}
		<span class="truncate">{current?.name ?? 'Places'}</span>
		{#if current?.readonly}<span class="badge bg-surface-2 text-muted max-md:hidden">Read-only</span>{/if}
		<ChevronDown class="m-chev size-4 shrink-0 text-muted" /><!-- [M25] -->
		{#if current?.smart?.status === 'failing' || current?.smart?.status === 'warning'}
			<!-- [M41] -->
			<span class="m-pulse-dot size-2 shrink-0 rounded-full {current.smart.status === 'failing' ? 'bg-danger' : 'bg-warn'}"></span>
		{:else if current?.smart}
			<span class="size-2 shrink-0 rounded-full {current.smart.status === 'ok' ? 'bg-ok' : 'bg-muted'}"></span>
		{/if}
	{/snippet}
	{#if places.list.length > 8}
		<input class="input mb-2" placeholder="Filter places" bind:value={filter} aria-label="Filter places" />
	{/if}
	<ul class="flex flex-col" role="listbox" aria-label="Places" data-fresh>
		{#each shown as p, i (p.id)}
			{@const used = p.disk && p.disk.total ? (p.disk.total - p.disk.free) / p.disk.total : 0}
			<!-- [M18] m-enter (the sheet sets data-fresh), [M42] m-grow-x -->
			<li class="m-enter" style:--i={i}>
				<div
					role="option"
					aria-selected={p.id === current?.id}
					tabindex="0"
					class="flex w-full cursor-pointer flex-col gap-1 rounded-md p-2 text-left transition-colors hover:bg-surface-2 {p.id === current?.id ? 'bg-surface-2' : ''}"
					onclick={() => pick(p)}
					onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && pick(p)}
				>
					<div class="flex items-center gap-2">
						<HardDrive class="size-4 text-muted" />
						<span class="truncate font-semibold">{p.name}</span>
						{#if p.readonly}<span class="badge bg-surface-2 text-muted">Read-only</span>{/if}
						<span class="ml-auto"><SmartBadge place={p} /></span>
					</div>
					{#if p.disk}
						<div class="h-1.5 overflow-hidden rounded-full bg-surface-2">
							<div
								class="m-grow-x h-full rounded-full {used > 0.97 ? 'bg-danger' : used > 0.9 ? 'bg-warn' : 'bg-accent'}"
								style:width="{used * 100}%"
							></div>
						</div>
						<div class="flex justify-between text-xs text-muted">
							<span>{formatBytes(p.disk.free)} free of {formatBytes(p.disk.total)}</span>
							{#if sameDrive(p)}<span>same drive as <em>{sameDrive(p)}</em></span>{/if}
						</div>
					{/if}
				</div>
			</li>
		{/each}
	</ul>
</Sheet>
