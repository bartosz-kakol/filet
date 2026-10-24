<script lang="ts">
	import { FolderOpen, RotateCw, X } from '@lucide/svelte';
	import { formatEta, formatSpeed } from '$lib/files/format';
	import { kindOf } from '$lib/files/kinds';
	import { dirHref } from '$lib/files/path';
	import type { UploadItem } from '$lib/upload/manager.svelte';
	import { uploads } from '$lib/upload/uploads.svelte';
	import { places } from '$lib/stores/places.svelte';
	import KindIcon from '../KindIcon.svelte';

	let { item }: { item: UploadItem } = $props();

	const pct = (x: number) => Math.floor(x * 100);
	const status = $derived.by(() => {
		switch (item.state) {
			case 'queued':
				return item.preflighting ? 'Checking…' : item.note || 'Queued';
			case 'hashing':
				return `Hashing ${pct(item.hashProgress)} %${item.hashSpeed ? ' · ' + formatSpeed(item.hashSpeed) : ''}`;
			case 'registering':
				return 'Starting…';
			case 'uploading': {
				const eta = item.speed ? formatEta((item.size - item.sentBytes) / item.speed) : '';
				return `Uploading ${pct(item.progress)} %${item.speed ? ' · ' + formatSpeed(item.speed) : ''}${eta ? ' · ' + eta : ''}`;
			}
			case 'waiting-network':
				return 'Waiting for connection…';
			case 'paused-auth':
				return 'Sign in to continue';
			case 'finalizing':
				return 'Finishing…';
			case 'done':
				return item.savedAs ? `Done — saved as ${item.savedAs}` : 'Done';
			case 'skipped':
				return 'Skipped';
			case 'cancelled':
				return 'Cancelled';
			case 'failed':
				return `Failed: ${item.message}`;
			case 'corrupted':
				return `Corrupted: ${item.message || 'a chunk failed verification 3 times'}`;
			case 'removed':
				return 'Removed on the server';
		}
		return '';
	});
	const tone = $derived(
		item.state === 'failed' || item.state === 'corrupted' || item.state === 'removed'
			? 'text-danger'
			: item.state === 'waiting-network' || item.state === 'paused-auth'
				? 'text-warn'
				: 'text-muted'
	);
	const folderLabel = $derived(`${places.name(item.place)}${item.dir === '/' ? '' : item.dir}`);
</script>

<div class="flex gap-3 px-3 py-2" data-testid="upload-row" data-state={item.state}>
	<KindIcon kind={kindOf(item.name)} class="mt-0.5 size-5" />
	<div class="min-w-0 flex-1">
		<div class="truncate text-sm font-medium" title={item.path}>{item.name}</div>
		<div class="truncate text-xs text-muted">
			{#if item.state === 'done'}
				<a class="hover:underline" href={dirHref(item.place, item.dir)}>{folderLabel}</a>
			{:else}{folderLabel}{/if}
		</div>
		<div class="truncate text-xs {tone}" title={status}>{status}</div>
		{#if item.note && item.state !== 'queued'}<div class="truncate text-xs text-muted" title={item.note}>{item.note}</div>{/if}
		{#if !item.terminal}
			<div class="mt-1 h-0.5 overflow-hidden rounded bg-surface-2" aria-label="Hashing progress">
				<div class="h-full bg-muted/60 transition-[width] duration-300" style:width="{item.hashProgress * 100}%"></div>
			</div>
			<div class="mt-0.5 h-1 overflow-hidden rounded bg-surface-2" aria-label="Upload progress">
				<div class="m-stripes h-full bg-accent transition-[width] duration-300" style:width="{item.progress * 100}%"></div><!-- [M29] -->
			</div>
		{/if}
	</div>
	<div class="flex shrink-0 items-start">
		{#if !item.terminal}
			<button class="icon-btn size-8" aria-label="Cancel upload" title="Cancel" onclick={() => uploads.cancel(item)}><X class="size-4" /></button>
		{:else if item.state === 'failed' || item.state === 'removed' || item.state === 'corrupted'}
			<button class="icon-btn size-8" aria-label="Retry upload" title="Retry" onclick={() => uploads.retry(item)}><RotateCw class="size-4" /></button>
		{:else if item.state === 'done'}
			<a
				class="icon-btn size-8"
				aria-label="Show in folder"
				title="Show in folder"
				href={dirHref(item.place, item.dir, { preview: item.savedAs ?? item.name })}><FolderOpen class="size-4" /></a
			>
		{/if}
	</div>
</div>
