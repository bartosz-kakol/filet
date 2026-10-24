<script lang="ts">
	import { Eye, EyeOff, LoaderCircle } from '@lucide/svelte';
	import { onDestroy } from 'svelte';
	import { ApiError, post } from '$lib/api/client';

	let { onSuccess, autofocus = true }: { onSuccess: () => void; autofocus?: boolean } = $props();

	let username = $state('');
	let password = $state('');
	let show = $state(false);
	let busy = $state(false);
	let error = $state<string | null>(null);
	let errorSeq = $state(0); // [M36] re-runs the shake for a repeated error
	let lockedUntil = $state(0);
	let now = $state(Date.now());
	let timer: ReturnType<typeof setInterval> | null = null;
	const remaining = $derived(Math.max(0, Math.ceil((lockedUntil - now) / 1000)));

	function startCountdown(ms: number) {
		lockedUntil = Date.now() + ms;
		now = Date.now();
		timer ??= setInterval(() => {
			now = Date.now();
			if (now >= lockedUntil && timer) {
				clearInterval(timer);
				timer = null;
			}
		}, 200);
	}

	onDestroy(() => timer && clearInterval(timer));

	async function submit(e: Event) {
		e.preventDefault();
		if (busy || remaining > 0) return;
		busy = true;
		error = null;
		try {
			await post('/api/auth/login', { username, password }, { noAuthRedirect: true });
			onSuccess();
		} catch (err) {
			if (err instanceof ApiError && err.code === 'login_locked') {
				startCountdown(Number(err.details.retryAfterMs ?? 5000));
			} else if (err instanceof ApiError && err.code === 'invalid_credentials') {
				error = 'Incorrect username or password';
				errorSeq++;
				password = '';
			} else {
				error = err instanceof Error ? err.message : 'Sign in failed';
				errorSeq++;
			}
		} finally {
			busy = false;
		}
	}
</script>

<form class="flex flex-col gap-3" onsubmit={submit}>
	<label class="flex flex-col gap-1 text-sm font-medium">
		Username
		<!-- svelte-ignore a11y_autofocus -->
		<input class="input" bind:value={username} autocomplete="username" autocapitalize="off" spellcheck="false" {autofocus} required />
	</label>
	<label class="flex flex-col gap-1 text-sm font-medium">
		Password
		<div class="relative">
			<input
				class="input pr-10"
				type={show ? 'text' : 'password'}
				bind:value={password}
				autocomplete="current-password"
				required
			/>
			<button
				type="button"
				class="icon-btn absolute top-0 right-0"
				aria-label={show ? 'Hide password' : 'Show password'}
				onclick={() => (show = !show)}
			>
				{#if show}<EyeOff class="m-swap size-4" />{:else}<Eye class="m-swap size-4" />{/if}<!-- [M37] -->
			</button>
		</div>
	</label>
	{#if error}{#key errorSeq}<p class="m-shake text-sm text-danger" role="alert">{error}</p>{/key}{/if}<!-- [M36] -->
	<button class="btn btn-primary mt-1" type="submit" disabled={busy || remaining > 0}>
		{#if busy}<LoaderCircle class="size-4 animate-spin" />{/if}
		{remaining > 0 ? `Try again in ${remaining} s` : 'Sign in'}
	</button>
</form>
