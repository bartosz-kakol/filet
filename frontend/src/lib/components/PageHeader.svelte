<script lang="ts">
	import { ArrowLeft } from '@lucide/svelte';
	import type { Snippet } from 'svelte';
	import { dirHref } from '$lib/files/path';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import AppMenu from './AppMenu.svelte';

	let { title, actions, placeId }: { title: string; actions?: Snippet; placeId?: string } = $props();
	const back = $derived(prefs.lastPlace ? dirHref(prefs.lastPlace, '/') : '/');
</script>

<header class="flex h-14 items-center gap-2 border-b border-line px-2 md:px-3">
	<a class="icon-btn" href={back} aria-label="Back to files"><ArrowLeft class="size-5" /></a>
	<h1 class="min-w-0 flex-1 truncate text-base font-semibold">{title}</h1>
	{@render actions?.()}
	<AppMenu {placeId} />
</header>
