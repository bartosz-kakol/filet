<script lang="ts">
	import { ChevronDown } from '@lucide/svelte';
	import type { Source } from '$lib/api/source';
	import type { Entry, MediaInfo } from '$lib/api/types';
	import { formatBytes, formatDateTime, formatDuration } from '$lib/files/format';
	import { joinPath } from '$lib/files/path';
	import { fetchInfo } from '$lib/preview/prefetch';
	import { summarizeExif, type ExifSummary } from '$lib/preview/exif';

	let { src, dir, entry, dims = null }: { src: Source; dir: string; entry: Entry; dims?: { w: number; h: number } | null } = $props();
	const path = $derived(joinPath(dir, entry.name));
	const isImage = $derived(entry.kind === 'image' || entry.kind === 'image-render');
	const isMedia = $derived(entry.kind === 'video' || entry.kind === 'audio');

	let exif = $state<ExifSummary | null>(null);
	let exifAll = $state<[string, string][]>([]);
	let exifError = $state(false);
	let media = $state<MediaInfo | null>(null);
	let mediaError = $state<string | null>(null);
	let showAll = $state(false);

	$effect(() => {
		exif = null;
		media = null;
		if (isImage) {
			// exifr fetches only the byte ranges it needs ("chunked" mode).
			const opts = {
				tiff: true,
				exif: true,
				gps: true,
				iptc: true,
				xmp: true,
				icc: false,
				translateValues: true,
				reviveValues: true,
				chunked: true,
				firstChunkSize: 65536
			};
			import('exifr')
				.then(({ default: exifr }) => exifr.parse(src.raw(path), opts as Parameters<typeof exifr.parse>[1]))
				.then((tags: Record<string, unknown> | undefined) => {
					exif = summarizeExif(tags ?? {});
					exifAll = Object.entries(tags ?? {})
						.filter(([, v]) => v !== null && typeof v !== 'object' || v instanceof Date || Array.isArray(v))
						.map(([k, v]) => [k, v instanceof Date ? v.toISOString() : Array.isArray(v) ? v.join(', ') : String(v)] as [string, string]);
				})
				.catch(() => (exifError = true));
		}
		if (isMedia) {
			fetchInfo<MediaInfo>(src.mediaInfo(path))
				.then((m) => (media = m))
				.catch((e) => (mediaError = String(e?.message ?? e)));
		}
	});

	const rows = (pairs: [string, string | number | null | undefined][]) =>
		pairs.filter(([, v]) => v !== null && v !== undefined && v !== '') as [string, string | number][];
</script>

{#snippet table(pairs: [string, string | number][])}
	<dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
		{#each pairs as [k, v] (k)}
			<dt class="text-zinc-400">{k}</dt>
			<dd class="break-words">{v}</dd>
		{/each}
	</dl>
{/snippet}

<div class="flex flex-col gap-5 p-4" data-testid="info-panel">
	<section>
		<h3 class="mb-2 text-xs font-semibold tracking-wide text-zinc-400 uppercase">File</h3>
		{@render table(
			rows([
				['Name', entry.name],
				['Path', path],
				['Size', `${formatBytes(entry.size)} (${entry.size.toLocaleString()} bytes)`],
				['Modified', formatDateTime(entry.mtime)],
				['Type', entry.mime],
				['Permissions', entry.mode]
			])
		)}
	</section>

	{#if isImage}
		<section>
			<h3 class="mb-2 text-xs font-semibold tracking-wide text-zinc-400 uppercase">Photo</h3>
			{#if exif}
				{@const photoRows = rows(exif.rows)}
				{@const sized =
					dims && !photoRows.some(([k]) => k === 'Dimensions')
						? ([['Dimensions', `${dims.w}×${dims.h} (${((dims.w * dims.h) / 1e6).toFixed(1)} MP)`] as [string, string], ...photoRows])
						: photoRows}
				{@render table(sized)}
				{#if photoRows.length === 0}<p class="mt-1 text-sm text-zinc-400">No EXIF metadata</p>{/if}
				{#if exif.gps}
					<a
						class="mt-2 inline-block text-sm text-blue-400 underline"
						target="_blank"
						rel="noopener noreferrer"
						href="https://www.openstreetmap.org/?mlat={exif.gps.lat}&mlon={exif.gps.lon}#map=15/{exif.gps.lat}/{exif.gps.lon}">Open in OpenStreetMap</a
					>
				{/if}
				{#if exifAll.length}
					<button class="mt-3 flex items-center gap-1 text-sm text-zinc-400 hover:text-zinc-100" onclick={() => (showAll = !showAll)}>
						<ChevronDown class="size-4 {showAll ? 'rotate-180' : ''}" />All tags
					</button>
					{#if showAll}<div class="mt-2">{@render table(exifAll)}</div>{/if}
				{/if}
			{:else if exifError}
				{#if dims}{@render table([['Dimensions', `${dims.w}×${dims.h}`]])}{/if}
				<p class="text-sm text-zinc-400">No EXIF metadata</p>
			{:else}
				<p class="text-sm text-zinc-400">Reading metadata…</p>
			{/if}
		</section>
	{/if}

	{#if isMedia}
		{#if media}
			<section>
				<h3 class="mb-2 text-xs font-semibold tracking-wide text-zinc-400 uppercase">Media</h3>
				{@render table(
					rows([
						['Duration', formatDuration(media.duration)],
						['Container', media.container],
						['Bitrate', media.bitrate ? `${(media.bitrate / 1000).toFixed(0)} kb/s` : null],
						['Title', media.tags.title],
						['Artist', media.tags.artist],
						['Album', media.tags.album],
						['Album artist', media.tags.albumArtist],
						['Track', media.tags.track],
						['Date', media.tags.date],
						['Genre', media.tags.genre]
					])
				)}
			</section>
			{#each media.video as v (v.index)}
				<section>
					<h3 class="mb-2 text-xs font-semibold tracking-wide text-zinc-400 uppercase">Video #{v.index}</h3>
					{@render table(
						rows([
							['Codec', [v.codec, v.profile].filter(Boolean).join(' · ')],
							['Resolution', `${v.width}×${v.height}`],
							['Aspect', v.dar],
							['Frame rate', v.fps ? `${v.fps} fps` : null],
							['Bit depth', v.bitDepth ? `${v.bitDepth}-bit` : null],
							['HDR', v.hdr],
							['Rotation', v.rotation ? `${v.rotation}°` : null],
							['Bitrate', v.bitrate ? `${(v.bitrate / 1000).toFixed(0)} kb/s` : null]
						])
					)}
				</section>
			{/each}
			{#each media.audio as a (a.index)}
				<section>
					<h3 class="mb-2 text-xs font-semibold tracking-wide text-zinc-400 uppercase">Audio #{a.index}{a.default ? ' (default)' : ''}</h3>
					{@render table(
						rows([
							['Codec', [a.codec, a.profile].filter(Boolean).join(' · ')],
							['Channels', `${a.channels}${a.layout ? ` (${a.layout})` : ''}`],
							['Sample rate', a.sampleRate ? `${a.sampleRate} Hz` : null],
							['Language', a.language],
							['Title', a.title]
						])
					)}
				</section>
			{/each}
			{#if media.subtitles.length || media.sidecarSubtitles.length}
				<section>
					<h3 class="mb-2 text-xs font-semibold tracking-wide text-zinc-400 uppercase">Subtitles</h3>
					<ul class="text-sm">
						{#each media.subtitles as s (s.index)}
							<li>{[s.language, s.title, s.codec].filter(Boolean).join(' · ')}{s.text ? '' : ' — not supported in browser'}</li>
						{/each}
						{#each media.sidecarSubtitles as s (s.name)}<li>{s.name}</li>{/each}
					</ul>
				</section>
			{/if}
			{#if media.chapters.length}
				<section>
					<h3 class="mb-2 text-xs font-semibold tracking-wide text-zinc-400 uppercase">Chapters</h3>
					<ol class="text-sm">
						{#each media.chapters as c, i (i)}
							<li><span class="text-zinc-400 tabular-nums">{formatDuration(c.start)}</span> {c.title ?? `Chapter ${i + 1}`}</li>
						{/each}
					</ol>
				</section>
			{/if}
		{:else if mediaError}
			<p class="text-sm text-zinc-400">{mediaError}</p>
		{:else}
			<p class="text-sm text-zinc-400">Reading media information…</p>
		{/if}
	{/if}
</div>
