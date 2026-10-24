<script lang="ts">
	import { Check, Copy, Lock } from '@lucide/svelte';
	import { onMount } from 'svelte';
	import { del, errorMessage, get } from '$lib/api/client';
	import type { ShareLink } from '$lib/api/types';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import KindIcon from '$lib/components/KindIcon.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import { formatDateTime, formatRelative } from '$lib/files/format';
	import { kindOf } from '$lib/files/kinds';
	import { baseName } from '$lib/files/path';
	import { toasts } from '$lib/stores/toast.svelte';

	let links = $state<ShareLink[]>([]);
	let loaded = $state(false);
	let copied = $state<string | null>(null);
	let revokeTarget = $state<ShareLink | null>(null);
	let revokeOpen = $state(false);
	let cleanOpen = $state(false);

	async function load() {
		try {
			links = await get<ShareLink[]>('/api/share-links');
		} catch (e) {
			toasts.error(errorMessage(e));
		} finally {
			loaded = true;
		}
	}
	onMount(load);

	async function copy(l: ShareLink) {
		await navigator.clipboard.writeText(location.origin + l.url);
		copied = l.id;
		setTimeout(() => (copied = null), 1500);
	}

	async function revoke(l: ShareLink) {
		try {
			await del(`/api/share-links/${encodeURIComponent(l.id)}`);
		} catch (e) {
			toasts.error(errorMessage(e));
		}
		void load();
	}

	const broken = $derived(links.filter((l) => !l.targetExists || l.placeMissing || (l.expiresAt !== null && l.expiresAt < Date.now())));
	async function revokeBroken() {
		await Promise.all(broken.map((l) => del(`/api/share-links/${encodeURIComponent(l.id)}`).catch(() => {})));
		void load();
	}
</script>

<svelte:head><title>Share links · Filet</title></svelte:head>

<div class="flex h-dvh flex-col">
	<PageHeader title="Share links">
		{#snippet actions()}
			<button class="btn h-8" disabled={!broken.length} onclick={() => (cleanOpen = true)}>Revoke all expired / broken</button>
		{/snippet}
	</PageHeader>
	<div class="min-h-0 flex-1 overflow-auto">
		{#if loaded && !links.length}<p class="p-6 text-center text-muted">No share links</p>{/if}
		<table class="w-full text-sm">
			<thead class="sticky top-0 bg-bg text-left text-xs text-muted max-md:hidden">
				<tr class="border-b border-line">
					<th class="px-3 py-2 font-medium">Item</th><th class="py-2 font-medium">Created</th><th class="py-2 font-medium">Expires</th>
					<th class="py-2 font-medium">Last accessed</th><th class="py-2 text-right font-medium">Views</th><th></th>
				</tr>
			</thead>
			<tbody>
				{#each links as l (l.id)}
					<tr class="border-b border-line align-top max-md:flex max-md:flex-wrap" data-testid="link-row">
						<td class="px-3 py-2 max-md:w-full">
							<div class="flex items-center gap-2">
								<KindIcon kind={l.isDir ? 'dir' : kindOf(l.path)} />
								<span class="truncate font-medium {l.targetExists ? '' : 'text-muted line-through'}">{baseName(l.path) || l.placeName}</span>
								{#if l.hasPassword}<Lock class="size-3.5 text-muted" aria-label="Password protected" />{/if}
							</div>
							<div class="truncate text-xs text-muted">{l.placeMissing ? 'Place no longer configured' : `${l.placeName} · ${l.path}`}</div>
						</td>
						<td class="py-2 text-muted max-md:px-3" title={formatDateTime(l.createdAt)}>{formatRelative(l.createdAt)}</td>
						<td class="py-2 text-muted max-md:px-3">{l.expiresAt ? formatRelative(l.expiresAt) : 'Never'}</td>
						<td class="py-2 text-muted max-md:hidden">{l.lastAccessedAt ? formatRelative(l.lastAccessedAt) : '—'}</td>
						<td class="py-2 text-right text-muted tabular-nums max-md:hidden">{l.accessCount}</td>
						<td class="py-2 pr-3 text-right whitespace-nowrap max-md:ml-auto">
							<button class="btn h-8" onclick={() => copy(l)}>{#if copied === l.id}<Check class="size-4" />Copied{:else}<Copy class="size-4" />Copy link{/if}</button>
							<button class="btn h-8 text-danger" onclick={() => { revokeTarget = l; revokeOpen = true; }}>Revoke</button>
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
</div>

<ConfirmDialog bind:open={revokeOpen} title="Revoke this link?" description="People with the link lose access immediately." confirmLabel="Revoke" danger onConfirm={() => revokeTarget && revoke(revokeTarget)} />
<ConfirmDialog bind:open={cleanOpen} title="Revoke {broken.length} expired or broken link{broken.length === 1 ? '' : 's'}?" confirmLabel="Revoke" danger onConfirm={revokeBroken} />
