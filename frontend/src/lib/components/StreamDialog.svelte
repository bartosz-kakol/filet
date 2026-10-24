<script lang="ts">
	import { Check, Copy } from '@lucide/svelte';
	import { errorMessage, post } from '$lib/api/client';
	import { viewport } from '$lib/stores/media.svelte';
	import Modal from './Modal.svelte';

	let { open = $bindable(false), place, path }: { open?: boolean; place: string; path: string } = $props();
	let url = $state<string | null>(null);
	let expiresAt = $state(0);
	let error = $state<string | null>(null);
	let copied = $state(false);

	$effect(() => {
		if (!open) return;
		url = null;
		error = null;
		copied = false;
		post<{ url: string; expiresAt: number }>('/api/stream-links', { place, path })
			.then((r) => {
				url = location.origin + r.url;
				expiresAt = r.expiresAt;
			})
			.catch((e) => (error = errorMessage(e)));
	});

	const hours = $derived(Math.max(1, Math.round((expiresAt - Date.now()) / 3_600_000)));

	async function copy() {
		if (!url) return;
		await navigator.clipboard.writeText(url);
		copied = true;
	}
</script>

<Modal bind:open title="Open in external player" description="Paste this link into VLC, mpv, Infuse or any player that streams over HTTP.">
	{#if error}
		<p class="text-sm text-danger">{error}</p>
	{:else if !url}
		<p class="text-sm text-muted">Creating link…</p>
	{:else}
		<div class="flex gap-2">
			<input class="input font-mono text-xs" readonly value={url} aria-label="Stream link" />
			<button class="btn shrink-0" onclick={copy}>{#if copied}<Check class="size-4" />Copied{:else}<Copy class="size-4" />Copy{/if}</button>
		</div>
		<p class="text-sm text-muted">Valid for {hours} h</p>
		{#if viewport.mobile}
			<div class="flex gap-2">
				<a class="btn flex-1" href={`vlc://${url}`}>Open in VLC</a>
				<a class="btn flex-1" href={`infuse://x-callback-url/play?url=${encodeURIComponent(url)}`}>Open in Infuse</a>
			</div>
		{/if}
	{/if}
	{#snippet footer()}<button class="btn" onclick={() => (open = false)}>Close</button>{/snippet}
</Modal>
