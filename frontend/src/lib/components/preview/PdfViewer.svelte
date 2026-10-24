<script lang="ts">
	import { Download, ExternalLink } from '@lucide/svelte';
	import type { Source } from '$lib/api/source';
	import type { Entry } from '$lib/api/types';
	import { downloadFile } from '$lib/files/actions';

	let { src, path, entry }: { src: Source; path: string; entry: Entry } = $props();
	const inline = typeof navigator !== 'undefined' && (navigator as Navigator & { pdfViewerEnabled?: boolean }).pdfViewerEnabled === true;
</script>

{#if inline}
	<iframe title={entry.name} src={src.raw(path)} class="h-full w-full border-0 bg-white"></iframe>
{:else}
	<div class="flex h-full flex-col items-center justify-center gap-4 p-6">
		{#if entry.thumbable}
			<img src={src.thumb(path, entry)} alt="First page of {entry.name}" class="max-h-[50vh] rounded bg-white shadow" />
		{/if}
		<div class="flex gap-2">
			<a class="btn" href={src.raw(path)} target="_blank" rel="noopener"><ExternalLink class="size-4" />Open</a>
			<button class="btn" onclick={() => downloadFile(src, path)}><Download class="size-4" />Download</button>
		</div>
	</div>
{/if}
