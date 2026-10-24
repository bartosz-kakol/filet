<script lang="ts">
	import { Dialog } from 'bits-ui';
	import type { Snippet } from 'svelte';

	let {
		open = $bindable(false),
		title,
		description,
		children,
		footer,
		wide = false,
		onClose
	}: {
		open?: boolean;
		title: string;
		description?: string;
		children?: Snippet;
		footer?: Snippet;
		wide?: boolean;
		onClose?: () => void;
	} = $props();
</script>

<Dialog.Root
	bind:open
	onOpenChange={(o) => {
		if (!o) onClose?.();
	}}
>
	<Dialog.Portal>
		<Dialog.Overlay class="dialog-overlay" />
		<Dialog.Content class="dialog {wide ? 'md:w-[min(44rem,calc(100vw-2rem))]' : ''}">
			<Dialog.Title class="pr-6 text-base font-semibold break-words">{title}</Dialog.Title>
			{#if description}
				<Dialog.Description class="-mt-2 text-sm text-muted">{description}</Dialog.Description>
			{/if}
			{@render children?.()}
			{#if footer}
				<div class="flex flex-wrap justify-end gap-2">{@render footer()}</div>
			{/if}
		</Dialog.Content>
	</Dialog.Portal>
</Dialog.Root>
