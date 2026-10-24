<script lang="ts">
	import { RotateCw, Trash2 } from '@lucide/svelte';
	import { onMount } from 'svelte';
	import { del, errorMessage, get } from '$lib/api/client';
	import { events } from '$lib/api/events';
	import type { Leftover } from '$lib/api/types';
	import ConfirmDialog from '$lib/components/ConfirmDialog.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import { formatBytes, formatRelative } from '$lib/files/format';
	import { baseName, dirHref, parentPath } from '$lib/files/path';
	import { leftovers as badge } from '$lib/stores/leftovers.svelte';
	import { toasts } from '$lib/stores/toast.svelte';
	import { uploads } from '$lib/upload/uploads.svelte';

	let items = $state<Leftover[]>([]);
	let loaded = $state(false);
	let removeOne = $state<Leftover | null>(null);
	let removeOneOpen = $state(false);
	let removeAllOpen = $state(false);
	let resumeTarget: Leftover | null = null;
	let picker: HTMLInputElement | undefined = $state();

	async function load() {
		try {
			items = await get<Leftover[]>('/api/uploads/leftovers');
		} catch (e) {
			toasts.error(errorMessage(e));
		} finally {
			loaded = true;
			void badge.refresh();
		}
	}

	onMount(() => {
		void load();
		const offs = ['upload.registered', 'upload.completed', 'upload.removed'].map((t) => events.on(t, () => void load()));
		return () => offs.forEach((f) => f());
	});

	function resume(l: Leftover) {
		resumeTarget = l;
		picker?.click();
	}

	function picked(e: Event) {
		const input = e.currentTarget as HTMLInputElement;
		const f = input.files?.[0];
		input.value = '';
		const l = resumeTarget;
		if (!f || !l) return;
		if (f.name !== baseName(l.path) || f.size !== l.size) {
			toasts.error('This is not the same file (name or size differs)');
			return;
		}
		void uploads.add([{ file: f, place: l.place, path: l.path }], { expectResume: true });
	}

	async function remove(l: Leftover) {
		try {
			await del(`/api/uploads/${encodeURIComponent(l.id)}`);
		} catch (e) {
			toasts.error(errorMessage(e));
		}
		void load();
	}

	async function removeAll() {
		try {
			await del('/api/uploads/leftovers');
		} catch (e) {
			toasts.error(errorMessage(e));
		}
		void load();
	}
</script>

<svelte:head><title>Leftovers · Filet</title></svelte:head>
<input bind:this={picker} type="file" class="hidden" onchange={picked} data-testid="resume-input" />

<div class="flex h-dvh flex-col">
	<PageHeader title="Leftovers">
		{#snippet actions()}
			<button class="btn h-8" disabled={!items.length} onclick={() => (removeAllOpen = true)}>Remove all</button>
		{/snippet}
	</PageHeader>
	<p class="border-b border-line px-4 py-3 text-sm text-muted">
		Uploads that were interrupted. Resume by choosing the same file again, or by uploading it to the same folder.
	</p>
	<div class="min-h-0 flex-1 overflow-auto">
		{#if loaded && !items.length}
			<p class="p-6 text-center text-muted">No interrupted uploads</p>
		{/if}
		<ul>
			{#each items as l (l.id)}
				{@const pct = l.size ? Math.floor((l.receivedBytes / l.size) * 100) : 0}
				<li class="flex flex-wrap items-center gap-x-4 gap-y-1 border-b border-line px-4 py-3 text-sm" data-testid="leftover-row">
					<div class="min-w-0 flex-1">
						<div class="truncate font-medium">{baseName(l.path)}</div>
						<div class="truncate text-xs text-muted">
							{#if l.placeMissing}Place no longer configured{:else}<a class="hover:underline" href={dirHref(l.place, parentPath(l.path))}>{l.placeName}{parentPath(l.path) === '/' ? '' : parentPath(l.path)}</a>{/if}
						</div>
					</div>
					<div class="w-24 text-muted tabular-nums">{formatBytes(l.size)}</div>
					<div class="w-48 text-muted tabular-nums">{pct} % — {l.receivedCount} of {l.chunkCount} chunks</div>
					<div class="w-40 text-xs text-muted">started {formatRelative(l.createdAt)}<br />last activity {formatRelative(l.lastActivityAt)}</div>
					<div class="flex gap-1">
						{#if !l.placeMissing}
							<button class="btn h-8" onclick={() => resume(l)}><RotateCw class="size-4" />Resume</button>
						{/if}
						<button class="btn h-8" onclick={() => { removeOne = l; removeOneOpen = true; }}><Trash2 class="size-4" />Remove</button>
					</div>
				</li>
			{/each}
		</ul>
	</div>
</div>

<ConfirmDialog
	bind:open={removeOneOpen}
	title="Remove this interrupted upload?"
	description="Its partial data is deleted."
	confirmLabel="Remove"
	danger
	onConfirm={() => removeOne && remove(removeOne)}
/>
<ConfirmDialog
	bind:open={removeAllOpen}
	title="Remove {items.length} interrupted upload{items.length === 1 ? '' : 's'} and their partial data?"
	confirmLabel="Remove all"
	danger
	onConfirm={removeAll}
/>
