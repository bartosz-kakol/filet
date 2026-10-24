<script lang="ts">
	import { Copy, Download, Ellipsis, FolderInput, Link2, Pencil, Trash2, X } from '@lucide/svelte';
	import { DropdownMenu } from 'bits-ui';
	import type { Entry } from '$lib/api/types';
	import { canOpen } from '$lib/files/actions';
	import { viewport } from '$lib/stores/media.svelte';
	import type { MenuAction } from './ItemMenu.svelte';

	let {
		entries,
		readonly,
		isPublic,
		onAction,
		onClear
	}: { entries: Entry[]; readonly: boolean; isPublic: boolean; onAction: (a: MenuAction) => void; onClear: () => void } =
		$props();

	const one = $derived(entries.length === 1);
	const openable = $derived(entries.every(canOpen));
</script>

<!-- [M11] m-selbar, m-count-pop. Floats above the transfer panel when the window is too narrow for both side by side. -->
<div
	class="m-selbar fixed bottom-[calc(max(1rem,env(safe-area-inset-bottom))+var(--transfer-h,0px))] z-30 flex h-12 items-center gap-1 rounded-xl border border-line bg-surface px-2 text-sm shadow-lg transition-[bottom] duration-200 max-md:inset-x-3 md:left-4 min-[1080px]:bottom-4"
	role="toolbar" aria-label="Selection" data-testid="selection-bar">
	<button class="icon-btn" aria-label="Clear selection" onclick={onClear}><X class="size-5" /></button>
	<span class="mr-2 font-medium">{#key entries.length}<span class="m-count-pop">{entries.length}</span>{/key} selected</span>
	<button class="icon-btn" aria-label="Download" title="Download" disabled={!openable} onclick={() => onAction('download')}><Download class="size-4" /></button>
	{#if !isPublic}
		{#if viewport.mobile}
			<button class="icon-btn" aria-label="Move to" title="Move to…" disabled={readonly} onclick={() => onAction('move')}><FolderInput class="size-4" /></button>
			<DropdownMenu.Root>
				<DropdownMenu.Trigger class="icon-btn" aria-label="More actions"><Ellipsis class="m-rot90 size-4" /></DropdownMenu.Trigger>
				<DropdownMenu.Portal>
					<DropdownMenu.Content class="menu" align="end">
						<DropdownMenu.Item class="menu-item" disabled={!openable} onSelect={() => onAction('copy')}><Copy class="size-4" />Copy to…</DropdownMenu.Item>
						<DropdownMenu.Item class="menu-item" disabled={!one || !openable} onSelect={() => onAction('share')}><Link2 class="size-4" />Share link…</DropdownMenu.Item>
						<DropdownMenu.Item class="menu-item" disabled={!one || readonly} onSelect={() => onAction('rename')}><Pencil class="size-4" />Rename</DropdownMenu.Item>
						<DropdownMenu.Item class="menu-item text-danger" disabled={readonly} onSelect={() => onAction('delete')}><Trash2 class="size-4" />Delete</DropdownMenu.Item>
					</DropdownMenu.Content>
				</DropdownMenu.Portal>
			</DropdownMenu.Root>
		{:else}
			<button class="btn btn-ghost h-8" disabled={!openable} onclick={() => onAction('copy')}><Copy class="size-4" />Copy to</button>
			<button class="btn btn-ghost h-8" disabled={readonly} onclick={() => onAction('move')}><FolderInput class="size-4" />Move to</button>
			<button class="btn btn-ghost h-8" disabled={!one || !openable} onclick={() => onAction('share')}><Link2 class="size-4" />Share</button>
			<button class="btn btn-ghost h-8" disabled={!one || readonly} onclick={() => onAction('rename')}><Pencil class="size-4" />Rename</button>
			<button class="icon-btn text-danger" aria-label="Delete" title="Delete" disabled={readonly} onclick={() => onAction('delete')}><Trash2 class="size-4" /></button>
		{/if}
	{/if}
</div>
