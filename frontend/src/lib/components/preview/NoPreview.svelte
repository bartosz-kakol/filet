<script lang="ts">
	import { Download } from '@lucide/svelte';
	import type { Source } from '$lib/api/source';
	import type { Entry } from '$lib/api/types';
	import { downloadFile } from '$lib/files/actions';
	import { formatBytes, formatDateTime } from '$lib/files/format';
	import KindIcon from '../KindIcon.svelte';

	let { src, path, entry, message = 'No preview available' }: { src: Source; path: string; entry: Entry; message?: string } = $props();
</script>

<div class="flex h-full items-center justify-center p-6">
	<div class="flex w-full max-w-sm flex-col items-center gap-3 rounded-xl border border-white/10 bg-zinc-900 p-6 text-center">
		<KindIcon kind={entry.kind} class="size-12" strokeWidth={1.25} />
		<div class="font-medium break-all">{entry.name}</div>
		<div class="text-sm text-zinc-400">{formatBytes(entry.size)} · {formatDateTime(entry.mtime)}</div>
		<div class="text-sm text-zinc-400">{message}</div>
		<button class="btn" onclick={() => downloadFile(src, path)}><Download class="size-4" />Download</button>
	</div>
</div>
