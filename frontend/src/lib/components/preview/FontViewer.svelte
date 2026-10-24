<script lang="ts">
	import type { Source } from '$lib/api/source';
	import type { Entry } from '$lib/api/types';
	import type { ViewerControls } from '$lib/preview/controls.svelte';

	let { src, path, entry, controls }: { src: Source; path: string; entry: Entry; controls: ViewerControls } = $props();
	let family = $state<string | null>(null);
	let error = $state<string | null>(null);
	let sample = $state('The quick brown fox jumps over the lazy dog');
	let seq = 0;

	$effect(() => {
		const name = `filet-preview-${++seq}-${Date.now()}`;
		const face = new FontFace(name, `url("${src.raw(path)}")`);
		face
			.load()
			.then((f) => {
				document.fonts.add(f);
				family = name;
				controls.loaded = true;
			})
			.catch(() => (error = 'This font could not be loaded'));
		controls.actions = [];
		return () => document.fonts.delete(face);
	});
</script>

{#if error}
	<div class="flex h-full items-center justify-center text-sm text-zinc-400">{error}</div>
{:else if !family}
	<div class="flex h-full items-center justify-center text-sm text-zinc-400">Loading font…</div>
{:else}
	<div class="h-full overflow-auto p-6" style:font-family="'{family}'">
		<input class="input mb-6 bg-zinc-900 font-sans" bind:value={sample} aria-label="Sample text" />
		{#each [16, 24, 36, 48, 72] as size (size)}
			<p class="mb-4 break-words" style:font-size="{size}px"><span class="mr-3 font-sans text-xs text-zinc-500">{size}</span>{sample}</p>
		{/each}
		<p class="mt-8 text-3xl">ABCDEFGHIJKLMNOPQRSTUVWXYZ</p>
		<p class="text-3xl">abcdefghijklmnopqrstuvwxyz</p>
		<p class="text-3xl">0123456789</p>
		<p class="text-3xl">. , ; : ! ? ' " ( ) [ ] {'{'} {'}'} - – — / \ @ # $ % &amp; * + = &lt; &gt;</p>
		<p class="mt-6 font-sans text-xs text-zinc-500">{entry.name}</p>
	</div>
{/if}
