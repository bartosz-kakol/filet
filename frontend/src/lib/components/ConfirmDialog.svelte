<script lang="ts">
	import Modal from './Modal.svelte';

	let {
		open = $bindable(false),
		title,
		description,
		confirmLabel,
		danger = false,
		onConfirm
	}: {
		open?: boolean;
		title: string;
		description?: string;
		confirmLabel: string;
		danger?: boolean;
		onConfirm: () => void;
	} = $props();
</script>

<Modal bind:open {title} {description}>
	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>Cancel</button>
		<button
			class="btn {danger ? 'btn-danger' : 'btn-primary'}"
			onclick={() => {
				open = false;
				onConfirm();
			}}>{confirmLabel}</button
		>
	{/snippet}
</Modal>
