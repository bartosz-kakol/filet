<script lang="ts">
	import '../app.css';
	import { onMount, type Snippet } from 'svelte';
	import { onNavigate } from '$app/navigation';
	import Toasts from '$lib/components/Toasts.svelte';
	import { installRipple, viewTransition } from '$lib/motion';
	import { toasts } from '$lib/stores/toast.svelte';

	let { children }: { children: Snippet } = $props();

	// [M04]
	onMount(installRipple);

	// [M45] Cross-fade between pages (not between folders or previews of the same page).
	onNavigate((nav) => {
		if (!nav.from || !nav.to || nav.from.route.id === nav.to.route.id) return;
		return new Promise<void>((resolve) => {
			viewTransition(async () => {
				resolve();
				await nav.complete;
			});
		});
	});

	// Service worker with an update prompt (registerType "prompt").
	onMount(() => {
		if (import.meta.env.DEV || !('serviceWorker' in navigator)) return;
		let reloading = false;
		navigator.serviceWorker.addEventListener('controllerchange', () => {
			if (reloading) return;
			reloading = true;
			location.reload();
		});
		const offer = (w: ServiceWorker) =>
			toasts.show('A new version of Filet is available', 'info', {
				sticky: true,
				action: { label: 'Reload', run: () => w.postMessage({ type: 'SKIP_WAITING' }) }
			});
		navigator.serviceWorker
			.register('/service-worker.js', { type: 'module', scope: '/' })
			.then((reg) => {
				if (reg.waiting && navigator.serviceWorker.controller) offer(reg.waiting);
				reg.addEventListener('updatefound', () => {
					const w = reg.installing;
					w?.addEventListener('statechange', () => {
						if (w.state === 'installed' && navigator.serviceWorker.controller) offer(w);
					});
				});
				setInterval(() => reg.update().catch(() => {}), 60 * 60 * 1000);
			})
			.catch(() => {});
	});
</script>

{@render children()}
<Toasts />
