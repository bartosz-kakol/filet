<script lang="ts">
	import { Check, Copy, Eye, EyeOff } from '@lucide/svelte';
	import { errorMessage, post } from '$lib/api/client';
	import type { ShareLink } from '$lib/api/types';
	import { places } from '$lib/stores/places.svelte';
	import Modal from './Modal.svelte';

	let { open = $bindable(false), place, path, name }: { open?: boolean; place: string; path: string; name: string } = $props();

	const PRESETS: [string, number | null][] = [
		['1 hour', 3_600_000],
		['1 day', 86_400_000],
		['7 days', 7 * 86_400_000],
		['30 days', 30 * 86_400_000],
		['Never', null]
	];
	let expiry = $state('7 days');
	let custom = $state('');
	let password = $state('');
	let showPw = $state(false);
	let busy = $state(false);
	let error = $state<string | null>(null);
	let link = $state<ShareLink | null>(null);
	let copied = $state(false);

	$effect(() => {
		if (!open) return;
		link = null;
		error = null;
		password = '';
		copied = false;
		void places.loadSettings().then((s) => {
			const def = s.shareDefaultExpiryMs;
			const preset = PRESETS.find(([, ms]) => (def === 0 ? ms === null : ms === def));
			if (preset) expiry = preset[0];
			else {
				expiry = 'Custom';
				custom = toLocalInput(Date.now() + def);
			}
		});
	});

	function toLocalInput(ms: number) {
		const d = new Date(ms);
		d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
		return d.toISOString().slice(0, 16);
	}

	async function create() {
		busy = true;
		error = null;
		let expiresAt: number | null;
		if (expiry === 'Custom') {
			expiresAt = custom ? new Date(custom).getTime() : NaN;
			if (!Number.isFinite(expiresAt) || expiresAt <= Date.now()) {
				error = 'Choose a time in the future';
				busy = false;
				return;
			}
		} else {
			const ms = PRESETS.find(([l]) => l === expiry)?.[1] ?? null;
			expiresAt = ms === null ? null : Date.now() + ms;
		}
		try {
			link = await post<ShareLink>('/api/share-links', { place, path, expiresAt, password: password || undefined });
		} catch (e) {
			error = errorMessage(e);
		} finally {
			busy = false;
		}
	}

	async function copy() {
		if (!link) return;
		await navigator.clipboard.writeText(location.origin + link.url);
		copied = true;
	}
</script>

<Modal bind:open title="Share “{name}”">
	{#if link}
		<div class="flex gap-2">
			<input class="input font-mono text-xs" readonly value={location.origin + link.url} aria-label="Share link" data-testid="share-url" />
			<button class="btn shrink-0" onclick={copy}>{#if copied}<Check class="size-4" />Copied{:else}<Copy class="size-4" />Copy{/if}</button>
		</div>
		<p class="text-sm text-muted">
			{link.expiresAt ? `Available until ${new Date(link.expiresAt).toLocaleString()}` : 'Never expires'}{link.hasPassword ? ' · password protected' : ''}
		</p>
	{:else}
		<label class="flex flex-col gap-1 text-sm font-medium">
			Expires
			<select class="input" bind:value={expiry}>
				{#each PRESETS as [label] (label)}<option>{label}</option>{/each}
				<option>Custom</option>
			</select>
		</label>
		{#if expiry === 'Custom'}
			<input class="input" type="datetime-local" bind:value={custom} aria-label="Custom expiry" />
		{/if}
		<label class="flex flex-col gap-1 text-sm font-medium">
			Password (optional)
			<div class="relative">
				<input class="input pr-10" type={showPw ? 'text' : 'password'} bind:value={password} autocomplete="new-password" maxlength="256" />
				<button type="button" class="icon-btn absolute top-0 right-0" aria-label={showPw ? 'Hide password' : 'Show password'} onclick={() => (showPw = !showPw)}>
					{#if showPw}<EyeOff class="size-4" />{:else}<Eye class="size-4" />{/if}
				</button>
			</div>
		</label>
		{#if error}<p class="text-sm text-danger">{error}</p>{/if}
	{/if}
	{#snippet footer()}
		<a class="mr-auto self-center text-sm text-accent hover:underline" href="/links" onclick={() => (open = false)}>Manage share links</a>
		{#if link}
			<button class="btn btn-primary" onclick={() => (open = false)}>Done</button>
		{:else}
			<button class="btn" onclick={() => (open = false)}>Cancel</button>
			<button class="btn btn-primary" disabled={busy} onclick={create}>Create link</button>
		{/if}
	{/snippet}
</Modal>
