<script lang="ts">
	import { Check, Circle, EllipsisVertical, Link2, LogOut, Trash2, UploadCloud } from '@lucide/svelte';
	import { DropdownMenu } from 'bits-ui';
	import { goto } from '$app/navigation';
	import { post } from '$lib/api/client';
	import { badgeText } from '$lib/files/format';
	import { themeTransition } from '$lib/motion';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import type { Theme } from '$lib/prefs/storage';
	import { leftovers } from '$lib/stores/leftovers.svelte';
	import { places } from '$lib/stores/places.svelte';
	import { session } from '$lib/stores/session.svelte';

	let { placeId }: { placeId?: string } = $props();
	const badge = $derived(badgeText(leftovers.count));
	const trashPlace = $derived(
		places.byId(placeId)?.readonly === false ? placeId : (places.list.find((p) => !p.readonly)?.id ?? placeId)
	);

	// [M44] remembers where the theme item was clicked so the reveal starts there.
	let lastX: number | undefined;
	let lastY: number | undefined;

	async function signOut() {
		try {
			await post('/api/auth/logout');
		} catch {
			/* ignore */
		}
		try {
			await caches?.delete('filet-thumbs');
		} catch {
			/* no Cache API */
		}
		session.authenticated = false;
		await goto('/login');
	}
</script>

<DropdownMenu.Root>
	<DropdownMenu.Trigger class="icon-btn relative" aria-label="Menu" data-testid="app-menu">
		<EllipsisVertical class="m-rot90 size-5" /><!-- [M25] -->
		{#if badge}
			<!-- [M40] -->
			<span class="m-badge badge absolute -top-0.5 -right-0.5 bg-danger text-white" data-testid="leftover-badge">{badge}</span>
		{/if}
	</DropdownMenu.Trigger>
	<DropdownMenu.Portal>
		<DropdownMenu.Content class="menu w-60" align="end" sideOffset={6}>
			<DropdownMenu.Item class="menu-item" onSelect={() => trashPlace && goto(`/trash/${encodeURIComponent(trashPlace)}`)}>
				<Trash2 class="size-4" />Trash
			</DropdownMenu.Item>
			<DropdownMenu.Item class="menu-item" onSelect={() => goto('/leftovers')}>
				<UploadCloud class="size-4" />Leftovers
				{#if badge}<span class="badge ml-auto bg-danger text-white">{badge}</span>{/if}
			</DropdownMenu.Item>
			<DropdownMenu.Item class="menu-item" onSelect={() => goto('/links')}><Link2 class="size-4" />Share links</DropdownMenu.Item>
			<DropdownMenu.Separator class="my-1 h-px bg-line" />
			<DropdownMenu.CheckboxItem class="menu-item" bind:checked={prefs.showHidden} closeOnSelect={false}>
				{#snippet children({ checked })}
					<span class="flex size-4 items-center justify-center">{#if checked}<Check class="m-check size-4" />{/if}</span>Show hidden files
				{/snippet}
			</DropdownMenu.CheckboxItem>
			<DropdownMenu.Separator class="my-1 h-px bg-line" />
			<DropdownMenu.RadioGroup
				value={prefs.theme}
				onValueChange={(v) => themeTransition(() => (prefs.theme = v as Theme), lastX, lastY)}
				onpointerdown={(e: PointerEvent) => ((lastX = e.clientX), (lastY = e.clientY))}
			>
				<DropdownMenu.GroupHeading class="px-2 py-1 text-xs text-muted">Theme</DropdownMenu.GroupHeading>
				{#each [['system', 'System'], ['light', 'Light'], ['dark', 'Dark']] as [v, label] (v)}
					<DropdownMenu.RadioItem class="menu-item" value={v} closeOnSelect={false}>
						{#snippet children({ checked })}
							<span class="flex size-4 items-center justify-center">{#if checked}<Circle class="m-check size-2 fill-current" />{/if}</span>{label}
						{/snippet}
					</DropdownMenu.RadioItem>
				{/each}
			</DropdownMenu.RadioGroup>
			<DropdownMenu.Separator class="my-1 h-px bg-line" />
			<DropdownMenu.Item class="menu-item" onSelect={signOut}><LogOut class="size-4" />Sign out</DropdownMenu.Item>
		</DropdownMenu.Content>
	</DropdownMenu.Portal>
</DropdownMenu.Root>
