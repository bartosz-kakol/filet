<script lang="ts">
	import { FolderOpen, Upload, X } from '@lucide/svelte';
	import { del, entries as idbEntries, get as idbGet, set as idbSet } from 'idb-keyval';
	import { onDestroy } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import DestinationPicker from '$lib/components/DestinationPicker.svelte';
	import PageHeader from '$lib/components/PageHeader.svelte';
	import { formatBytes, formatRelative } from '$lib/files/format';
	import { kindOf } from '$lib/files/kinds';
	import { dirHref, joinPath } from '$lib/files/path';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import { places } from '$lib/stores/places.svelte';
	import type { UploadItem } from '$lib/upload/manager.svelte';
	import { uploads } from '$lib/upload/uploads.svelte';

	interface ShareRecord {
		files: File[];
		title: string;
		text: string;
		createdAt: number;
	}

	const id = $derived(page.url.searchParams.get('id'));
	const noSw = $derived(page.url.searchParams.get('error') === 'no-sw');
	let record = $state<ShareRecord | null>(null);
	let missing = $state(false);
	let pending = $state<{ id: string; rec: ShareRecord }[]>([]);
	let previews = $state<(string | null)[]>([]);
	const firstWritable = places.list.find((p) => p.id === prefs.lastPlace && !p.readonly) ?? places.list.find((p) => !p.readonly);
	let place = $state(firstWritable?.id ?? '');
	let dir = $state('/');
	let pickerOpen = $state(false);

	$effect(() => {
		const key = id;
		if (key) {
			idbGet<ShareRecord>(`share:${key}`).then((r) => {
				record = r ?? null;
				missing = !r;
				previews.forEach((u) => u && URL.revokeObjectURL(u));
				previews = (r?.files ?? []).map((f) => (kindOf(f.name) === 'image' ? URL.createObjectURL(f) : null));
			});
		} else {
			idbEntries().then((all) => {
				pending = all
					.filter(([k]) => typeof k === 'string' && k.startsWith('share:'))
					.map(([k, v]) => ({ id: (k as string).slice(6), rec: v as ShareRecord }))
					.sort((a, b) => b.rec.createdAt - a.rec.createdAt);
			});
		}
	});
	onDestroy(() => previews.forEach((u) => u && URL.revokeObjectURL(u)));

	async function cancel() {
		if (id) await del(`share:${id}`);
		void goto('/');
	}

	async function uploadHere() {
		if (!record || !id || !place) return;
		const key = `share:${id}`;
		const files = [...record.files];
		const settled = new Set<File>();
		const onSettled = async (it: UploadItem) => {
			if (it.state === 'done' || it.state === 'skipped' || it.state === 'cancelled') settled.add(it.file);
			const left = files.filter((f) => !settled.has(f));
			if (!left.length) await del(key);
			else await idbSet(key, { ...record, files: left });
		};
		void uploads.add(
			files.map((f) => ({ file: f, place, path: joinPath(dir, f.name) })),
			{ onSettled: (it) => void onSettled(it) }
		);
		prefs.panelCollapsed = false;
		void goto(dirHref(place, dir));
	}

	const placeName = $derived(places.byId(place)?.name ?? '');
</script>

<svelte:head><title>Upload shared files · Filet</title></svelte:head>

<div class="flex min-h-dvh flex-col">
	<PageHeader title="Upload shared files" />
	<div class="mx-auto flex w-full max-w-2xl flex-col gap-4 p-4">
		{#if noSw}
			<p class="rounded-lg border border-line bg-surface p-3 text-sm">Filet wasn't ready to receive the share. Open Filet once, then share again.</p>
		{/if}
		{#if !id}
			{#if pending.length}
				<h2 class="font-semibold">Pending shares</h2>
				<ul class="flex flex-col gap-2">
					{#each pending as p (p.id)}
						<li class="flex items-center gap-3 rounded-lg border border-line p-3 text-sm">
							<span class="flex-1">{p.rec.files.length} file{p.rec.files.length === 1 ? '' : 's'} · {formatRelative(p.rec.createdAt)}</span>
							<a class="btn h-8" href="/share?id={p.id}">Continue</a>
							<button class="btn h-8" onclick={async () => { await del(`share:${p.id}`); pending = pending.filter((x) => x.id !== p.id); }}>Discard</button>
						</li>
					{/each}
				</ul>
			{:else if !noSw}
				<p class="text-muted">Nothing is waiting to be uploaded.</p>
			{/if}
		{:else if missing}
			<p class="text-muted">This share was already handled or has expired.</p>
		{:else if record}
			{#if !record.files.length}
				<p class="text-muted">Only files can be shared to Filet</p>
			{:else}
				<ul class="flex flex-col gap-2" data-testid="shared-files">
					{#each record.files as f, i (i)}
						<li class="flex items-center gap-3 rounded-lg border border-line p-2 text-sm">
							{#if previews[i]}<img src={previews[i]} alt="" class="size-12 rounded object-cover" />{/if}
							<span class="min-w-0 flex-1 truncate">{f.name}</span>
							<span class="text-muted">{formatBytes(f.size)}</span>
						</li>
					{/each}
				</ul>
				<div class="flex items-center gap-2 rounded-lg border border-line p-3 text-sm">
					<FolderOpen class="size-4 text-accent" />
					<span class="min-w-0 flex-1 truncate">{placeName}{dir === '/' ? '' : dir}</span>
					<button class="btn h-8" onclick={() => (pickerOpen = true)}>Change…</button>
				</div>
				<div class="flex justify-end gap-2">
					<button class="btn" onclick={cancel}><X class="size-4" />Cancel</button>
					<button class="btn btn-primary" disabled={!place} onclick={uploadHere}><Upload class="size-4" />Upload here</button>
				</div>
			{/if}
		{/if}
	</div>
</div>

<DestinationPicker
	bind:open={pickerOpen}
	title="Upload to…"
	confirmLabel="Upload here"
	initialPlace={place}
	initialDir={dir}
	onPick={(p, d) => {
		place = p;
		dir = d;
	}}
/>
