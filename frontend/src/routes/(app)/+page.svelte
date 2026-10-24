<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { dirHref } from '$lib/files/path';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import { places } from '$lib/stores/places.svelte';

	onMount(() => {
		const id = places.byId(prefs.lastPlace)?.id ?? places.list[0]?.id;
		if (id) void goto(dirHref(id, '/'), { replaceState: true });
	});
</script>

{#if !places.list.length}
	<div class="flex h-dvh items-center justify-center text-muted">No places configured</div>
{/if}
