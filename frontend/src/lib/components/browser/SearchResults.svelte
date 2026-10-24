<script lang="ts">
	import { goto } from '$app/navigation';
	import { toApiError } from '$lib/api/client';
	import type { SearchDone, SearchHit } from '$lib/api/types';
	import { formatBytes, formatDate } from '$lib/files/format';
	import { compareNames } from '$lib/files/sort';
	import { dirHref, parentPath } from '$lib/files/path';
	import { prefs } from '$lib/prefs/prefs.svelte';
	import KindIcon from '../KindIcon.svelte';

	let { place, q }: { place: string; q: string } = $props();

	let hits = $state<SearchHit[]>([]);
	let done = $state<SearchDone | null>(null);
	let error = $state<string | null>(null);

	$effect(() => {
		const ac = new AbortController();
		void run(place, q, prefs.showHidden, ac.signal);
		return () => ac.abort();
	});

	async function run(place: string, q: string, hidden: boolean, signal: AbortSignal) {
		hits = [];
		done = null;
		error = null;
		try {
			const res = await fetch(
				`/api/places/${encodeURIComponent(place)}/search?q=${encodeURIComponent(q)}&hidden=${hidden ? 1 : 0}&limit=500`,
				{ signal, credentials: 'same-origin' }
			);
			if (!res.ok) {
				error = (await toApiError(res)).message;
				return;
			}
			const reader = res.body!.pipeThrough(new TextDecoderStream()).getReader();
			let buf = '';
			let batch: SearchHit[] = [];
			for (;;) {
				const { value, done: end } = await reader.read();
				if (value) buf += value;
				let nl: number;
				while ((nl = buf.indexOf('\n')) >= 0) {
					const line = buf.slice(0, nl);
					buf = buf.slice(nl + 1);
					if (!line) continue;
					const obj = JSON.parse(line);
					if (obj.done) done = obj;
					else batch.push(obj);
				}
				if (batch.length) {
					hits.push(...batch);
					batch = [];
				}
				if (end) break;
			}
			hits = [...hits].sort((a, b) => compareNames(a.path, b.path));
		} catch (e) {
			if ((e as Error).name !== 'AbortError') error = String(e);
		}
	}

	function open(h: SearchHit) {
		if (h.entry.type === 'dir') void goto(dirHref(place, h.path));
		else void goto(dirHref(place, parentPath(h.path), { preview: h.entry.name }));
	}

	const status = $derived(
		error
			? error
			: !done
				? `Searching… ${hits.length} found`
				: done.truncated
					? `Showing first ${hits.length} results`
					: done.timedOut
						? `Search stopped after 60 s · ${hits.length} results`
						: `${hits.length} result${hits.length === 1 ? '' : 's'}`
	);
</script>

<div class="flex flex-col">
	<div class="px-3 py-2 text-sm text-muted" data-testid="search-status">{status}</div>
	<ul>
		{#each hits as h (h.path)}
			<li>
				<button class="flex w-full items-center gap-3 px-3 py-2 text-left hover:bg-surface" onclick={() => open(h)} data-testid="search-hit">
					<KindIcon kind={h.entry.type === 'dir' ? 'dir' : h.entry.kind} />
					<div class="min-w-0 flex-1">
						<div class="truncate text-sm">{h.entry.name}</div>
						<div class="truncate text-xs text-muted">{parentPath(h.path)}</div>
					</div>
					<span class="hidden w-24 text-right text-sm text-muted md:block">{h.entry.type === 'dir' ? '—' : formatBytes(h.entry.size)}</span>
					<span class="hidden w-32 text-sm text-muted md:block">{formatDate(h.entry.mtime)}</span>
				</button>
			</li>
		{/each}
	</ul>
</div>
