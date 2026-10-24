<script lang="ts">
	import {
		Captions,
		Download,
		ListVideo,
		Maximize,
		Music,
		Pause,
		PictureInPicture2,
		Play,
		RotateCcw,
		RotateCw,
		Tv,
		Volume2,
		VolumeX,
		X,
		AudioLines,
		Gauge
	} from '@lucide/svelte';
	import { DropdownMenu } from 'bits-ui';
	import { onDestroy } from 'svelte';
	import type { Source } from '$lib/api/source';
	import type { Entry, MediaInfo, Place } from '$lib/api/types';
	import { downloadFile } from '$lib/files/actions';
	import { formatDuration } from '$lib/files/format';
	import type { ViewerControls } from '$lib/preview/controls.svelte';
	import { CODEC_STRINGS, codecLabel, decidePlayback, type Playback } from '$lib/preview/media';
	import { fetchInfo } from '$lib/preview/prefetch';
	import { displayTime, parseVtt, planSeek, shiftCues, type Cue } from '$lib/preview/remux';
	import StreamDialog from '../StreamDialog.svelte';

	let { src, path, entry, controls, place }: { src: Source; path: string; entry: Entry; controls: ViewerControls; place: Place | null } =
		$props();

	let info = $state<MediaInfo | null>(null);
	let error = $state<string | null>(null);
	let playback = $state<Playback | null>(null);
	let el = $state<HTMLVideoElement | HTMLAudioElement | undefined>();
	let container = $state<HTMLDivElement | undefined>();
	let cw = $state(0);
	let ch = $state(0);
	let rot = $state(0);
	let bannerOpen = $state(true);
	let streamOpen = $state(false);

	// remux state
	let offset = $state(0);
	let videoIdx = $state(-1);
	let audioIdx = $state(-1);
	let current = $state(0);
	let paused = $state(true);
	let volume = $state(1);
	let muted = $state(false);
	let rate = $state(1);
	let seeking = $state<number | null>(null);
	let streamUrl = $state<string | null>(null);

	// subtitles
	type SubChoice = { key: string; label: string; lang: string; stream?: number; sidecar?: string };
	let subChoice = $state<SubChoice | null>(null);
	let subCues: Cue[] = [];
	let activeTrack: TextTrack | null = null;

	const isAudioKind = $derived(entry.kind === 'audio' || (!!info && info.video.length === 0));
	const remux = $derived(playback?.mode === 'remux' || playback?.mode === 'remux-video-only');
	const duration = $derived(info?.duration ?? 0);
	const shown = $derived(seeking ?? displayTime(offset, current));

	$effect(() => {
		const url = src.mediaInfo(path);
		fetchInfo<MediaInfo>(url)
			.then((i) => {
				info = i;
				const probe = document.createElement(i.video.length && entry.kind === 'video' ? 'video' : 'audio');
				playback = decidePlayback(entry.name, i, (t) => probe.canPlayType(t));
				if (playback.mode === 'remux') {
					videoIdx = playback.video;
					audioIdx = playback.audio;
				} else if (playback.mode === 'remux-video-only') {
					videoIdx = playback.video;
					audioIdx = -1;
				}
				if (playback.mode === 'unsupported') controls.loaded = true;
				else if (playback.mode !== 'direct') restart(0, false);
			})
			.catch((e) => {
				// Without ffprobe, try the browser directly.
				error = String(e?.message ?? e);
				playback = { mode: 'direct', audio: -1 };
			});
	});

	function restart(t: number, play = !paused) {
		offset = Math.max(0, t);
		current = 0;
		streamUrl = src.remux(path, offset, videoIdx, audioIdx);
		queueMicrotask(() => {
			if (!el) return;
			el.load();
			if (play) el.play().catch(() => {});
			rebuildSubs();
		});
	}

	function seekTo(t: number) {
		t = Math.max(0, Math.min(duration || t, t));
		if (!el) return;
		if (!remux) {
			el.currentTime = t;
			return;
		}
		const ranges: [number, number][] = [];
		for (let i = 0; i < el.buffered.length; i++) ranges.push([el.buffered.start(i), el.buffered.end(i)]);
		const plan = planSeek(t, offset, ranges);
		if (plan.kind === 'local') el.currentTime = plan.currentTime;
		else restart(plan.offset);
	}

	function togglePlay() {
		if (!el) return;
		if (el.paused) el.play().catch(() => {});
		else el.pause();
	}

	// --- subtitles ---
	const subChoices = $derived.by((): SubChoice[] => {
		if (!info) return [];
		const out: SubChoice[] = [];
		for (const s of info.subtitles) {
			out.push({
				key: 's' + s.index,
				label: [s.title, s.language, s.text ? null : '(not supported in browser)'].filter(Boolean).join(' · ') || `Track ${s.index}`,
				lang: s.language ?? '',
				stream: s.text ? s.index : undefined
			});
		}
		for (const s of info.sidecarSubtitles) out.push({ key: 'f' + s.name, label: s.name, lang: s.language ?? '', sidecar: s.name });
		return out;
	});

	async function chooseSub(c: SubChoice | null) {
		subChoice = c;
		subCues = [];
		if (c && (c.stream !== undefined || c.sidecar)) {
			try {
				const r = await fetch(src.subtitle(path, c.sidecar ? { sidecar: c.sidecar } : { stream: c.stream }), { credentials: 'same-origin' });
				if (!r.ok) throw new Error(String(r.status));
				subCues = parseVtt(await r.text());
			} catch {
				subCues = [];
			}
		}
		rebuildSubs();
	}

	function rebuildSubs() {
		if (activeTrack) activeTrack.mode = 'disabled';
		activeTrack = null;
		if (!el || !subChoice || !subCues.length) return;
		const t = el.addTextTrack('subtitles', subChoice.label, subChoice.lang);
		for (const c of shiftCues(subCues, remux ? offset : 0)) t.addCue(new VTTCue(c.start, c.end, c.text));
		t.mode = 'showing';
		activeTrack = t;
	}

	// --- audio tracks available in remux mode ---
	const audioTracks = $derived.by(() => {
		if (!info || !remux) return [];
		const probe = document.createElement('audio');
		return info.audio.filter((a) => CODEC_STRINGS[a.codec] && probe.canPlayType(`audio/mp4; codecs="${CODEC_STRINGS[a.codec]}"`) !== '');
	});

	function chooseAudio(i: number) {
		audioIdx = i;
		restart(displayTime(offset, current));
	}

	// --- rotation & toolbar ---
	const rotate = (d: number) => (rot = (rot + d + 360) % 360);
	const turned = $derived(rot % 180 !== 0);
	$effect(() => {
		controls.actions = isAudioKind
			? []
			: [
					{ id: 'rl', label: 'Rotate left', icon: RotateCcw, onclick: () => rotate(-90) },
					{ id: 'rr', label: 'Rotate right', icon: RotateCw, onclick: () => rotate(90) }
				];
		controls.onKey = (e) => {
			if (isAudioKind) return false;
			if (e.key === 'r') rotate(90);
			else if (e.key === 'R') rotate(-90);
			else if (e.key === ' ' && remux) togglePlay();
			else return false;
			return true;
		};
	});

	onDestroy(() => {
		if (el) {
			el.pause();
			el.removeAttribute('src');
			el.load();
		}
	});

	const coverUrl = $derived(entry.thumbable && info?.hasCoverArt ? src.thumb(path, entry) : null);
	const directUrl = $derived(src.raw(path));
	const mediaStyle = $derived(
		turned && cw && ch ? `width:${ch}px;height:${cw}px;transform:translate(-50%,-50%) rotate(${rot}deg)` : `width:100%;height:100%;transform:translate(-50%,-50%) rotate(${rot}deg)`
	);
</script>

<div class="relative flex h-full w-full flex-col" bind:this={container}>
	{#if playback?.mode === 'remux-video-only' && bannerOpen}
		<div class="flex items-start gap-2 bg-amber-500/15 px-3 py-2 text-sm text-amber-200" role="status">
			<span class="flex-1">
				This file's audio ({codecLabel(playback.audioCodec)}) can't be played in the browser without re-encoding. Use
				<em>Open in external player</em> for sound.
			</span>
			<button class="icon-btn size-7 text-amber-200 hover:bg-white/10" aria-label="Dismiss" onclick={() => (bannerOpen = false)}><X class="size-4" /></button>
		</div>
	{/if}

	{#if !playback}
		<div class="flex flex-1 items-center justify-center text-sm text-zinc-400">Loading…</div>
	{:else if playback.mode === 'unsupported'}
		<div class="flex flex-1 items-center justify-center p-6">
			<div class="flex max-w-md flex-col items-center gap-3 text-center">
				<p class="text-zinc-300">This format can't be played in the browser.</p>
				{#if info}
					<p class="text-sm text-zinc-400">
						{info.video[0] ? `${info.video[0].codec} ${info.video[0].width}×${info.video[0].height}` : ''}
						{info.audio[0] ? ` · ${info.audio[0].codec} ${info.audio[0].channels} ch` : ''} · {formatDuration(info.duration)}
					</p>
				{/if}
				<div class="flex gap-2">
					{#if !src.isPublic && place}
						<button class="btn" onclick={() => (streamOpen = true)}><Tv class="size-4" />Open in external player</button>
					{/if}
					<button class="btn" onclick={() => downloadFile(src, path)}><Download class="size-4" />Download</button>
				</div>
			</div>
		</div>
	{:else}
		<div class="relative min-h-0 flex-1 overflow-hidden" bind:clientWidth={cw} bind:clientHeight={ch}>
			{#if isAudioKind}
				<div class="flex h-full flex-col items-center justify-center gap-4 p-6 text-center">
					{#if coverUrl}
						<img src={coverUrl} alt="Cover art" class="size-56 rounded-lg object-cover shadow-lg" />
					{:else}
						<div class="flex size-56 items-center justify-center rounded-lg bg-zinc-800"><Music class="size-20 text-zinc-500" /></div>
					{/if}
					<div>
						<div class="text-lg font-semibold">{info?.tags.title || entry.name}</div>
						{#if info?.tags.artist}<div class="text-zinc-300">{info.tags.artist}</div>{/if}
						{#if info?.tags.album}<div class="text-sm text-zinc-400">{info.tags.album}</div>{/if}
					</div>
					{#if playback.mode === 'direct'}
						<!-- svelte-ignore a11y_media_has_caption -->
						<audio bind:this={el} src={directUrl} controls autoplay class="w-full max-w-md" onloadedmetadata={() => (controls.loaded = true)}></audio>
					{:else}
						<!-- svelte-ignore a11y_media_has_caption -->
						<audio
							bind:this={el}
							src={streamUrl}
							autoplay
							bind:currentTime={current}
							bind:paused
							bind:volume
							bind:muted
							bind:playbackRate={rate}
							onloadedmetadata={() => (controls.loaded = true)}
						></audio>
					{/if}
				</div>
			{:else if playback.mode === 'direct'}
				<!-- svelte-ignore a11y_media_has_caption -->
				<video
					bind:this={el}
					src={directUrl}
					controls
					autoplay
					playsinline
					class="absolute top-1/2 left-1/2 bg-black object-contain"
					style={mediaStyle}
					data-testid="video"
					onloadedmetadata={() => {
						controls.loaded = true;
						rebuildSubs();
					}}
				></video>
			{:else}
				<!-- svelte-ignore a11y_media_has_caption -->
				<video
					bind:this={el}
					src={streamUrl}
					autoplay
					playsinline
					class="absolute top-1/2 left-1/2 bg-black object-contain"
					style={mediaStyle}
					data-testid="video"
					data-mode="remux"
					bind:currentTime={current}
					bind:paused
					bind:volume
					bind:muted
					bind:playbackRate={rate}
					onclick={togglePlay}
					onloadedmetadata={() => (controls.loaded = true)}
				></video>
			{/if}
		</div>

		{#if remux || (playback.mode === 'direct' && subChoices.length && !isAudioKind)}
			<div class="flex flex-wrap items-center gap-1 border-t border-white/10 px-2 py-1 text-sm" data-testid="media-controls">
				{#if remux}
					<button class="icon-btn text-zinc-100 hover:bg-white/10" aria-label={paused ? 'Play' : 'Pause'} onclick={togglePlay}>
						{#if paused}<Play class="size-5" />{:else}<Pause class="size-5" />{/if}
					</button>
					<span class="w-28 text-xs tabular-nums text-zinc-300" data-testid="media-time">{formatDuration(shown)} / {formatDuration(duration)}</span>
					<input
						type="range"
						min="0"
						max={duration || 1}
						step="0.1"
						value={shown}
						aria-label="Seek"
						class="min-w-24 flex-1 accent-blue-400"
						oninput={(e) => (seeking = Number((e.currentTarget as HTMLInputElement).value))}
						onchange={(e) => {
							const t = Number((e.currentTarget as HTMLInputElement).value);
							seeking = null;
							seekTo(t);
						}}
					/>
					<button class="icon-btn text-zinc-100 hover:bg-white/10" aria-label={muted ? 'Unmute' : 'Mute'} onclick={() => (muted = !muted)}>
						{#if muted || volume === 0}<VolumeX class="size-4" />{:else}<Volume2 class="size-4" />{/if}
					</button>
					<input type="range" min="0" max="1" step="0.05" bind:value={volume} aria-label="Volume" class="w-20 accent-blue-400 max-md:hidden" />
					<DropdownMenu.Root>
						<DropdownMenu.Trigger class="icon-btn text-zinc-100 hover:bg-white/10" aria-label="Playback speed"><Gauge class="size-4" /></DropdownMenu.Trigger>
						<DropdownMenu.Portal>
							<DropdownMenu.Content class="menu dark z-50" side="top">
								{#each [0.5, 0.75, 1, 1.25, 1.5, 2] as r (r)}
									<DropdownMenu.Item class="menu-item {rate === r ? 'font-semibold' : ''}" onSelect={() => (rate = r)}>{r}×</DropdownMenu.Item>
								{/each}
							</DropdownMenu.Content>
						</DropdownMenu.Portal>
					</DropdownMenu.Root>
					{#if audioTracks.length > 1}
						<DropdownMenu.Root>
							<DropdownMenu.Trigger class="icon-btn text-zinc-100 hover:bg-white/10" aria-label="Audio track"><AudioLines class="size-4" /></DropdownMenu.Trigger>
							<DropdownMenu.Portal>
								<DropdownMenu.Content class="menu dark z-50" side="top">
									{#each audioTracks as a (a.index)}
										<DropdownMenu.Item class="menu-item {audioIdx === a.index ? 'font-semibold' : ''}" onSelect={() => chooseAudio(a.index)}>
											{[a.title, a.language, a.codec, `${a.channels} ch`].filter(Boolean).join(' · ')}
										</DropdownMenu.Item>
									{/each}
								</DropdownMenu.Content>
							</DropdownMenu.Portal>
						</DropdownMenu.Root>
					{/if}
				{/if}
				{#if subChoices.length}
					<DropdownMenu.Root>
						<DropdownMenu.Trigger class="icon-btn text-zinc-100 hover:bg-white/10 {subChoice ? 'bg-white/15' : ''}" aria-label="Subtitles"><Captions class="size-4" /></DropdownMenu.Trigger>
						<DropdownMenu.Portal>
							<DropdownMenu.Content class="menu dark z-50 max-w-80" side="top">
								<DropdownMenu.Item class="menu-item" onSelect={() => chooseSub(null)}>Off</DropdownMenu.Item>
								{#each subChoices as c (c.key)}
									<DropdownMenu.Item class="menu-item" disabled={c.stream === undefined && !c.sidecar} onSelect={() => chooseSub(c)}>
										<span class="truncate {subChoice?.key === c.key ? 'font-semibold' : ''}">{c.label}</span>
									</DropdownMenu.Item>
								{/each}
							</DropdownMenu.Content>
						</DropdownMenu.Portal>
					</DropdownMenu.Root>
				{/if}
				{#if remux && info?.chapters.length}
					<DropdownMenu.Root>
						<DropdownMenu.Trigger class="icon-btn text-zinc-100 hover:bg-white/10" aria-label="Chapters"><ListVideo class="size-4" /></DropdownMenu.Trigger>
						<DropdownMenu.Portal>
							<DropdownMenu.Content class="menu dark z-50 max-h-80 overflow-auto" side="top">
								{#each info.chapters as c, i (i)}
									<DropdownMenu.Item class="menu-item" onSelect={() => seekTo(c.start)}>
										<span class="text-muted tabular-nums">{formatDuration(c.start)}</span>{c.title ?? `Chapter ${i + 1}`}
									</DropdownMenu.Item>
								{/each}
							</DropdownMenu.Content>
						</DropdownMenu.Portal>
					</DropdownMenu.Root>
				{/if}
				{#if remux && !isAudioKind}
					{#if 'pictureInPictureEnabled' in document && document.pictureInPictureEnabled}
						<button class="icon-btn text-zinc-100 hover:bg-white/10" aria-label="Picture in picture" onclick={() => (el as HTMLVideoElement)?.requestPictureInPicture().catch(() => {})}><PictureInPicture2 class="size-4" /></button>
					{/if}
					<button class="icon-btn text-zinc-100 hover:bg-white/10" aria-label="Fullscreen" onclick={() => container?.requestFullscreen().catch(() => {})}><Maximize class="size-4" /></button>
				{/if}
			</div>
		{/if}
	{/if}
	{#if error && playback?.mode === 'direct'}
		<p class="px-3 py-1 text-xs text-zinc-500">Media details unavailable: {error}</p>
	{/if}
</div>

{#if place && !src.isPublic}
	<StreamDialog bind:open={streamOpen} place={place.id} {path} />
{/if}
