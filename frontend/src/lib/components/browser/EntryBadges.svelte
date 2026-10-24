<script lang="ts">
	import { Link2, Link2Off, Lock } from '@lucide/svelte';
	import type { Entry } from '$lib/api/types';

	let { entry }: { entry: Entry } = $props();
	const denied = $derived(!entry.readable || entry.error === 'permission_denied');
</script>

{#if entry.symlink?.status === 'ok'}
	<Link2 class="size-3.5 shrink-0 text-muted" aria-label="Symbolic link" />
{:else if entry.symlink}
	<Link2Off class="size-3.5 shrink-0 text-danger" aria-label="Broken link" />
{/if}
{#if denied && !entry.symlink}
	<span title="Permission denied" class="shrink-0"><Lock class="size-3.5 text-muted" aria-label="Permission denied" /></span>
{/if}
