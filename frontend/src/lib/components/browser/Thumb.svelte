<script lang="ts">
	import { Play } from '@lucide/svelte';
	import type { Kind } from '$lib/api/types';
	import KindIcon from '../KindIcon.svelte';

	let { src, kind, alt = '' }: { src: string | null; kind: Kind; alt?: string } = $props();
	let loaded = $state(false);
	let failed = $state(false);

	$effect(() => {
		void src;
		loaded = false;
		failed = false;
	});

	// Clearing src on unmount aborts the in-flight request.
	function abortOnDestroy(img: HTMLImageElement) {
		return { destroy: () => img.removeAttribute('src') };
	}
</script>

<div class="relative flex h-full w-full items-center justify-center overflow-hidden bg-surface-2">
	{#if !loaded}
		<KindIcon {kind} class="size-10" strokeWidth={1.25} />
	{/if}
	<!-- [M17] m-thumb fades/sharpens in once loaded -->
	{#if src && !failed}
		<img
			{src}
			{alt}
			width="400"
			height="300"
			loading="lazy"
			decoding="async"
			draggable="false"
			use:abortOnDestroy
			class="m-thumb absolute inset-0 h-full w-full object-cover"
			data-loaded={loaded || undefined}
			onload={() => (loaded = true)}
			onerror={() => (failed = true)}
		/>
	{/if}
	{#if kind === 'video'}
		<span class="absolute right-2 bottom-2 flex size-6 items-center justify-center rounded-full bg-black/60 text-white">
			<Play class="size-3 fill-current" />
		</span>
	{/if}
</div>
