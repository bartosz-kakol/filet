<script lang="ts">
	import { Download, Lock } from '@lucide/svelte';
	import { page } from '$app/state';
	import { ApiError, get, post } from '$lib/api/client';
	import { Source } from '$lib/api/source';
	import type { Entry, PublicShareInfo } from '$lib/api/types';
	import Breadcrumbs from '$lib/components/Breadcrumbs.svelte';
	import Browser from '$lib/components/browser/Browser.svelte';
	import PreviewOverlay from '$lib/components/preview/PreviewOverlay.svelte';
	import { downloadZip } from '$lib/files/actions';
	import { listingCache } from '$lib/files/listing.svelte';
	import { encodeSegments } from '$lib/files/path';
	import Toasts from '$lib/components/Toasts.svelte';

	const token = $derived(page.params.token ?? '');
	const path = $derived('/' + (page.params.path ?? '').split('/').filter(Boolean).join('/'));
	let info = $state<PublicShareInfo | null>(null);
	let invalid = $state(false);
	let password = $state('');
	let pwError = $state<string | null>(null);
	let lockedUntil = $state(0);
	let busy = $state(false);

	async function load() {
		try {
			info = await get<PublicShareInfo>(`/api/public/share/${encodeURIComponent(token)}`, { noAuthRedirect: true });
			invalid = false;
		} catch {
			invalid = true;
		}
	}

	$effect(() => {
		void token;
		void load();
	});

	const src = $derived(info ? Source.share(token, info.name) : null);
	const hrefFor = (dir: string, q?: Record<string, string>) => {
		const tail = encodeSegments(dir);
		return `/s/${encodeURIComponent(token)}${tail ? '/' + tail : ''}${q ? '?' + new URLSearchParams(q) : ''}`;
	};

	async function unlock(e: Event) {
		e.preventDefault();
		if (busy || Date.now() < lockedUntil) return;
		busy = true;
		pwError = null;
		try {
			await post(`/api/public/share/${encodeURIComponent(token)}/unlock`, { password }, { noAuthRedirect: true });
			await load();
		} catch (err) {
			if (err instanceof ApiError && err.code === 'login_locked') {
				lockedUntil = Date.now() + Number(err.details.retryAfterMs ?? 5000);
				pwError = 'Too many attempts; try again in a few seconds';
			} else pwError = 'Incorrect password';
		} finally {
			busy = false;
		}
	}

	const fileEntry = $derived<Entry | null>(
		info && !info.isDir
			? {
					name: info.name,
					type: 'file',
					size: info.size,
					mtime: info.mtime,
					mode: '0644',
					readable: true,
					hidden: false,
					kind: info.kind,
					mime: info.mime ?? 'application/octet-stream',
					thumbable: info.thumbable ?? false,
					error: null,
					badName: false
				}
			: null
	);

	const until = $derived(info?.expiresAt ? new Date(info.expiresAt).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' }) : null);

	async function downloadAll() {
		if (!src) return;
		const l = await listingCache.fetch(src, path);
		downloadZip(src, path, l.entries.filter((e) => !e.hidden).map((e) => e.name));
	}
</script>

<svelte:head><title>{info?.name ?? 'Shared'} · Filet</title></svelte:head>

{#snippet slimHeader()}
	<header class="flex flex-col border-b border-line">
		<div class="flex h-14 items-center gap-3 px-3">
			<img src="/favicon.svg" alt="" class="size-6" />
			<div class="min-w-0 flex-1">
				<div class="truncate font-semibold">{info?.name}</div>
				{#if until}<div class="text-xs text-muted">Available until {until}</div>{/if}
			</div>
			{#if info?.isDir}
				<button class="btn h-8" onclick={downloadAll}><Download class="size-4" />Download all</button>
			{/if}
		</div>
		{#if info?.isDir}
			<div class="flex h-10 items-center px-2"><Breadcrumbs rootLabel={info.name} {path} href={(d) => hrefFor(d)} /></div>
		{/if}
	</header>
{/snippet}

{#if invalid}
	<main class="flex min-h-dvh flex-col items-center justify-center gap-2 p-6 text-center">
		<img src="/favicon.svg" alt="" class="size-10" />
		<p class="text-lg">This link is invalid or has expired.</p>
	</main>
{:else if !info}
	<main class="flex min-h-dvh items-center justify-center text-muted">Loading…</main>
{:else if info.passwordRequired && !info.unlocked}
	<main class="flex min-h-dvh items-center justify-center bg-surface p-4">
		<form class="flex w-full max-w-sm flex-col gap-3 rounded-2xl border border-line bg-bg p-6" onsubmit={unlock}>
			<div class="flex items-center gap-2 font-semibold"><Lock class="size-5" />{info.name}</div>
			<p class="text-sm text-muted">This shared item is password protected.</p>
			<!-- svelte-ignore a11y_autofocus -->
			<input class="input" type="password" bind:value={password} aria-label="Password" autocomplete="off" autofocus required />
			{#if pwError}<p class="text-sm text-danger">{pwError}</p>{/if}
			<button class="btn btn-primary" disabled={busy}>Unlock</button>
		</form>
	</main>
{:else if src && info.isDir}
	<div class="flex min-h-dvh flex-col">
		<div class="min-h-0 flex-1">
			<Browser
				{src}
				place={null}
				{path}
				rootLabel={info.name}
				{hrefFor}
				previewName={page.url.searchParams.get('preview')}
				publicHeader={slimHeader}
			/>
		</div>
	</div>
	<footer class="fixed bottom-2 left-1/2 -translate-x-1/2 text-xs text-muted">Shared with Filet</footer>
{:else if src && fileEntry}
	<PreviewOverlay
		{src}
		place={null}
		dir="/"
		entry={fileEntry}
		sequence={[fileEntry]}
		onStep={() => {}}
		onClose={() => {}}
		{hrefFor}
		closable={false}
	/>
{/if}
<Toasts />
