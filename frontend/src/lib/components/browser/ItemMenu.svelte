<script lang="ts">
	import { Copy, Download, ExternalLink, FolderInput, FolderOpen, Link2, Pencil, Trash2, Tv } from '@lucide/svelte';
	import { DropdownMenu } from 'bits-ui';
	import type { Entry } from '$lib/api/types';
	import { canOpen } from '$lib/files/actions';

	export type MenuAction = 'open' | 'download' | 'rename' | 'copy' | 'move' | 'share' | 'stream' | 'delete';

	let {
		open = $bindable(false),
		x,
		y,
		entries,
		readonly,
		isPublic,
		onAction
	}: {
		open?: boolean;
		x: number;
		y: number;
		entries: Entry[];
		readonly: boolean;
		isPublic: boolean;
		onAction: (a: MenuAction) => void;
	} = $props();

	const anchor = $derived({
		getBoundingClientRect: () => DOMRect.fromRect({ x, y, width: 0, height: 0 })
	});
	const one = $derived(entries.length === 1 ? entries[0] : null);
	const openable = $derived(entries.every(canOpen));
	const brokenLinks = $derived(entries.some((e) => e.symlink && e.symlink.status !== 'ok'));
	const isMedia = $derived(!!one && one.type === 'file' && (one.kind === 'video' || one.kind === 'audio'));

	// bits-ui selects an item on pointerup when the press began elsewhere. The menu opens on the press of a
	// right-click (or Ctrl+click, or a long-press), so that gesture's release could land on an item and pick it.
	// Only a press that starts inside the menu may select.
	let pressedInside = false;
	$effect(() => {
		if (open) pressedInside = false;
	});
</script>

<DropdownMenu.Root bind:open>
	<DropdownMenu.Portal>
		<DropdownMenu.Content
			class="menu"
			customAnchor={anchor}
			side="bottom"
			align="start"
			sideOffset={2}
			onpointerdowncapture={() => (pressedInside = true)}
			onpointerupcapture={(e: PointerEvent) => {
				if (!pressedInside) e.preventDefault();
			}}
		>
			{#if one}
				<DropdownMenu.Item class="menu-item" disabled={!canOpen(one)} onSelect={() => onAction('open')}>
					<FolderOpen class="size-4" />Open
				</DropdownMenu.Item>
			{/if}
			<DropdownMenu.Item class="menu-item" disabled={!openable} onSelect={() => onAction('download')}>
				<Download class="size-4" />Download
			</DropdownMenu.Item>
			{#if !isPublic}
				{#if one}
					<DropdownMenu.Item class="menu-item" disabled={readonly || one.badName} onSelect={() => onAction('rename')}>
						<Pencil class="size-4" />Rename
					</DropdownMenu.Item>
				{/if}
				<DropdownMenu.Item class="menu-item" disabled={!openable || brokenLinks} onSelect={() => onAction('copy')}>
					<Copy class="size-4" />Copy to…
				</DropdownMenu.Item>
				<DropdownMenu.Item class="menu-item" disabled={readonly} onSelect={() => onAction('move')}>
					<FolderInput class="size-4" />Move to…
				</DropdownMenu.Item>
				{#if one}
					<DropdownMenu.Item class="menu-item" disabled={!canOpen(one)} onSelect={() => onAction('share')}>
						<Link2 class="size-4" />Share link…
					</DropdownMenu.Item>
				{/if}
				{#if isMedia}
					<DropdownMenu.Item class="menu-item" disabled={!canOpen(one!)} onSelect={() => onAction('stream')}>
						<Tv class="size-4" />Open in external player
					</DropdownMenu.Item>
				{/if}
				<DropdownMenu.Separator class="my-1 h-px bg-line" />
				<DropdownMenu.Item class="menu-item text-danger" disabled={readonly} onSelect={() => onAction('delete')}>
					<Trash2 class="size-4" />Delete
				</DropdownMenu.Item>
			{:else if one && one.type === 'file'}
				<DropdownMenu.Item class="menu-item" onSelect={() => onAction('open')}><ExternalLink class="size-4" />Preview</DropdownMenu.Item>
			{/if}
		</DropdownMenu.Content>
	</DropdownMenu.Portal>
</DropdownMenu.Root>
