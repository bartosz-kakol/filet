<script lang="ts">
	import { tick } from 'svelte';
	import { validateName } from '$lib/files/path';
	import Modal from './Modal.svelte';

	let {
		open = $bindable(false),
		title,
		initial = '',
		confirmLabel,
		selectStem = false,
		onSubmit
	}: {
		open?: boolean;
		title: string;
		initial?: string;
		confirmLabel: string;
		/** Preselect the name up to (not including) the last extension. */
		selectStem?: boolean;
		/** Returns an error message to show inline, or null on success. */
		onSubmit: (name: string) => Promise<string | null>;
	} = $props();

	let name = $state('');
	let serverError = $state<string | null>(null);
	let busy = $state(false);
	let input: HTMLInputElement | undefined = $state();
	const error = $derived(name === initial && initial !== '' ? null : name ? validateName(name) : null);

	$effect(() => {
		if (open) {
			name = initial;
			serverError = null;
			void tick().then(() => {
				if (!input) return;
				input.focus();
				const dot = initial.lastIndexOf('.');
				if (selectStem && dot > 0) input.setSelectionRange(0, dot);
				else input.select();
			});
		}
	});

	async function submit(e: Event) {
		e.preventDefault();
		if (busy || error || !name.trim()) return;
		if (name === initial) {
			open = false;
			return;
		}
		busy = true;
		serverError = await onSubmit(name);
		busy = false;
		if (!serverError) open = false;
	}
</script>

<Modal bind:open {title}>
	<form onsubmit={submit} class="flex flex-col gap-3">
		<input
			bind:this={input}
			bind:value={name}
			class="input"
			aria-label="Name"
			autocomplete="off"
			spellcheck="false"
			oninput={() => (serverError = null)}
		/>
		{#if error || serverError}
			<p class="-mt-1 text-sm text-danger">{error ?? serverError}</p>
		{/if}
		<div class="flex justify-end gap-2">
			<button type="button" class="btn" onclick={() => (open = false)}>Cancel</button>
			<button type="submit" class="btn btn-primary" disabled={busy || !!error || !name.trim()}>{confirmLabel}</button>
		</div>
	</form>
</Modal>
