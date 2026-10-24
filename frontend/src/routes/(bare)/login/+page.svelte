<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import LoginForm from '$lib/components/LoginForm.svelte';
	import { session } from '$lib/stores/session.svelte';

	const next = $derived.by(() => {
		const n = page.url.searchParams.get('next') ?? '/';
		return n.startsWith('/') && !n.startsWith('//') ? n : '/';
	});

	onMount(async () => {
		if (await session.check()) void goto(next, { replaceState: true });
	});

	function done() {
		session.authenticated = true;
		// A full navigation re-runs the app bootstrap with the new session.
		location.assign(next);
	}
</script>

<svelte:head><title>Sign in · Filet</title></svelte:head>

<main class="flex min-h-dvh items-center justify-center bg-surface p-4">
	<!-- [M36] m-login-card, m-login-logo -->
	<div class="m-login-card w-full max-w-sm rounded-2xl border border-line bg-bg p-6 shadow-sm">
		<div class="mb-6 flex items-center justify-center gap-2 text-xl font-semibold">
			<img src="/favicon.svg" alt="" class="m-login-logo size-8" />Filet
		</div>
		<LoginForm onSuccess={done} />
	</div>
</main>
