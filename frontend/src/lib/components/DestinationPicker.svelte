<script lang="ts">
	import { ChevronRight, Folder, FolderPlus, CornerUpLeft } from '@lucide/svelte';
	import { Select } from 'bits-ui';
	import { untrack } from 'svelte';
	import { errorMessage, get, post } from '$lib/api/client';
	import type { Listing } from '$lib/api/types';
	import { compareNames } from '$lib/files/sort';
	import { joinPath, parentPath, validateName } from '$lib/files/path';
	import { places } from '$lib/stores/places.svelte';
	import Modal from './Modal.svelte';

	let {
		open = $bindable(false),
		title,
		confirmLabel,
		initialPlace,
		initialDir = '/',
		writableOnly = true,
		onPick
	}: {
		open?: boolean;
		title: string;
		confirmLabel: string;
		initialPlace: string;
		initialDir?: string;
		writableOnly?: boolean;
		onPick: (place: string, dir: string) => void;
	} = $props();

	let place = $state('');
	let dir = $state('/');
	let folders = $state<string[]>([]);
	let error = $state<string | null>(null);
	let loading = $state(false);
	let newName = $state<string | null>(null);

	const options = $derived(places.list.map((p) => ({ value: p.id, label: p.name, disabled: writableOnly && p.readonly })));
	const current = $derived(places.byId(place));

	// Reset when the dialog opens (only `open` is tracked).
	$effect(() => {
		if (!open) return;
		untrack(() => {
			const start = places.byId(initialPlace);
			place = start && !(writableOnly && start.readonly) ? initialPlace : (places.list.find((p) => !p.readonly)?.id ?? initialPlace);
			dir = place === initialPlace ? initialDir : '/';
			newName = null;
		});
	});

	$effect(() => {
		if (open && place) void load(place, dir);
	});

	async function load(p: string, d: string) {
		loading = true;
		error = null;
		try {
			const l = await get<Listing>(`/api/places/${encodeURIComponent(p)}/list?path=${encodeURIComponent(d)}`);
			folders = l.entries
				.filter((e) => e.type === 'dir' && !e.hidden && e.readable && !e.badName)
				.map((e) => e.name)
				.sort(compareNames);
		} catch (e) {
			error = errorMessage(e);
			folders = [];
		} finally {
			loading = false;
		}
	}

	async function createFolder(e: Event) {
		e.preventDefault();
		if (!newName) return;
		const msg = validateName(newName);
		if (msg) {
			error = msg;
			return;
		}
		try {
			await post(`/api/places/${encodeURIComponent(place)}/mkdir`, { path: joinPath(dir, newName) });
			dir = joinPath(dir, newName);
			newName = null;
		} catch (err) {
			error = errorMessage(err);
		}
	}
</script>

<Modal bind:open {title}>
	<Select.Root type="single" value={place} onValueChange={(v) => { place = v; dir = '/'; }} items={options}>
		<Select.Trigger class="input flex items-center justify-between text-left" aria-label="Place">
			{current?.name ?? 'Choose a place'}
			<ChevronRight class="size-4 rotate-90 text-muted" />
		</Select.Trigger>
		<Select.Portal>
			<Select.Content class="menu z-[60] w-[var(--bits-select-anchor-width)]" sideOffset={4}>
				{#each options as o (o.value)}
					<Select.Item class="menu-item" value={o.value} label={o.label} disabled={o.disabled}>
						{o.label}{#if o.disabled}<span class="ml-auto text-xs text-muted" title="Read-only">Read-only</span>{/if}
					</Select.Item>
				{/each}
			</Select.Content>
		</Select.Portal>
	</Select.Root>
	<div class="flex items-center gap-2 text-sm">
		<button class="icon-btn size-8" aria-label="Parent folder" disabled={dir === '/'} onclick={() => (dir = parentPath(dir))}><CornerUpLeft class="size-4" /></button>
		<span class="min-w-0 flex-1 truncate font-mono text-xs" title={dir}>{current?.name}{dir === '/' ? '' : dir}</span>
		<button class="btn h-8" onclick={() => (newName = '')}><FolderPlus class="size-4" />New folder</button>
	</div>
	{#if newName !== null}
		<form class="flex gap-2" onsubmit={createFolder}>
			<!-- svelte-ignore a11y_autofocus -->
			<input class="input" bind:value={newName} placeholder="Folder name" aria-label="New folder name" autofocus />
			<button class="btn" type="submit">Create</button>
		</form>
	{/if}
	<ul class="h-64 overflow-auto rounded-md border border-line" aria-label="Folders">
		{#if loading && !folders.length}
			<li class="p-3 text-sm text-muted">Loading…</li>
		{:else if !folders.length}
			<li class="p-3 text-sm text-muted">No folders here</li>
		{/if}
		{#each folders as f (f)}
			<li>
				<button class="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-surface-2" onclick={() => (dir = joinPath(dir, f))}>
					<Folder class="size-4 text-accent" /><span class="truncate">{f}</span>
				</button>
			</li>
		{/each}
	</ul>
	{#if error}<p class="text-sm text-danger">{error}</p>{/if}
	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>Cancel</button>
		<button
			class="btn btn-primary"
			disabled={!current || (writableOnly && current.readonly)}
			onclick={() => {
				open = false;
				onPick(place, dir);
			}}>{confirmLabel}</button
		>
	{/snippet}
</Modal>
