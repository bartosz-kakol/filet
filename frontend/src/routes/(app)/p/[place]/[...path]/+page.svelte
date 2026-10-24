<script lang="ts">
	import { page } from '$app/state';
	import { Source } from '$lib/api/source';
	import Browser from '$lib/components/browser/Browser.svelte';
	import { dirHref } from '$lib/files/path';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import { places } from '$lib/stores/places.svelte';

	const placeId = $derived(page.params.place ?? '');
	const place = $derived(places.byId(placeId));
	const path = $derived('/' + (page.params.path ?? '').split('/').filter(Boolean).join('/'));
	// One Source per place so the browser does not reload on query changes.
	const src = $derived(place ? Source.place(place.id, place.name) : null);

	$effect(() => {
		if (place) prefs.lastPlace = place.id;
	});
</script>

<svelte:head><title>{path === '/' ? place?.name : path.slice(path.lastIndexOf('/') + 1)} · Filet</title></svelte:head>

{#if place && src}
	<Browser
		{src}
		{place}
		{path}
		rootLabel={place.name}
		hrefFor={(d, q) => dirHref(place.id, d, q)}
		q={page.url.searchParams.get('q') ?? ''}
		previewName={page.url.searchParams.get('preview')}
	/>
{:else}
	<div class="flex h-dvh flex-col items-center justify-center gap-3 text-muted">
		<p>Unknown place “{placeId}”</p>
		<a class="btn" href="/">Go home</a>
	</div>
{/if}
