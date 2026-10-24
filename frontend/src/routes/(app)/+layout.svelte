<script lang="ts">
	import { onMount, type Snippet } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { setUnauthorizedHandler } from '$lib/api/client';
	import { events } from '$lib/api/events';
	import ConflictDialog from '$lib/components/ConflictDialog.svelte';
	import ReloginDialog from '$lib/components/ReloginDialog.svelte';
	import { jobs } from '$lib/jobs/jobs.svelte';
	import { leftovers } from '$lib/stores/leftovers.svelte';
	import { places } from '$lib/stores/places.svelte';
	import { session } from '$lib/stores/session.svelte';
	import { uploads } from '$lib/upload/uploads.svelte';

	let { children }: { children: Snippet } = $props();
	let ready = $state(false);

	function toLogin() {
		const next = page.url.pathname + page.url.search;
		void goto(`/login?next=${encodeURIComponent(next)}`, { replaceState: true });
	}

	onMount(() => {
		setUnauthorizedHandler(() => {
			if (uploads.hasActive || jobs.active.length) session.reloginOpen = true;
			else {
				session.authenticated = false;
				toLogin();
			}
		});
		void (async () => {
			if (!(await session.check())) return toLogin();
			await Promise.all([places.refresh(), places.loadSettings()]).catch(() => {});
			ready = true;
			events.start(() => session.check());
			jobs.start();
			leftovers.start();
			for (const t of ['upload.registered', 'upload.completed', 'upload.removed']) events.on(t, () => void leftovers.refresh());
			events.on('smart.updated', () => void places.refresh());
			events.onOpen(() => void leftovers.refresh());
		})();
		return () => setUnauthorizedHandler(null);
	});
</script>

{#if ready}
	{@render children()}
	{#if uploads.items.length || jobs.list.length}
		{#await import('$lib/components/panel/TransferPanel.svelte') then { default: TransferPanel }}
			<TransferPanel />
		{/await}
	{/if}
	<ConflictDialog />
	<ReloginDialog />
{:else}
	<div class="flex h-dvh items-center justify-center text-muted" aria-busy="true"><span class="m-loading-dots">Loading</span><!-- [M50] --></div>
{/if}
