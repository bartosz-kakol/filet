<script lang="ts">
	// Popover on desktop, bottom sheet on mobile.
	import { Dialog, Popover } from 'bits-ui';
	import type { Snippet } from 'svelte';
	import { viewport } from '$lib/stores/media.svelte';

	let {
		open = $bindable(false),
		label,
		trigger,
		children,
		triggerClass = '',
		align = 'start',
		contentClass = 'w-96'
	}: {
		open?: boolean;
		label: string;
		trigger: Snippet;
		children: Snippet;
		triggerClass?: string;
		align?: 'start' | 'center' | 'end';
		contentClass?: string;
	} = $props();
</script>

{#if viewport.mobile}
	<Dialog.Root bind:open>
		<Dialog.Trigger class={triggerClass} aria-label={label}>{@render trigger()}</Dialog.Trigger>
		<Dialog.Portal>
			<Dialog.Overlay class="dialog-overlay" />
			<!-- [M08] m-sheet -->
			<Dialog.Content class="m-sheet fixed inset-x-0 bottom-0 z-50 max-h-[85vh] overflow-auto rounded-t-2xl border border-line bg-bg p-2 pb-[max(0.5rem,env(safe-area-inset-bottom))] shadow-xl outline-none">
				<Dialog.Title class="sr-only">{label}</Dialog.Title>
				<div class="mx-auto mb-2 h-1 w-10 rounded-full bg-line"></div>
				{@render children()}
			</Dialog.Content>
		</Dialog.Portal>
	</Dialog.Root>
{:else}
	<Popover.Root bind:open>
		<Popover.Trigger class={triggerClass} aria-label={label}>{@render trigger()}</Popover.Trigger>
		<Popover.Portal>
			<Popover.Content class="menu max-h-[80vh] overflow-auto p-2 {contentClass}" sideOffset={6} {align}>
				{@render children()}
			</Popover.Content>
		</Popover.Portal>
	</Popover.Root>
{/if}
