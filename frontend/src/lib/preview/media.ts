import type { MediaInfo } from '$lib/api/types';
import { ext } from '$lib/files/kinds';

/** ffprobe codec → codecs string used with canPlayType. */
export const CODEC_STRINGS: Record<string, string> = {
	h264: 'avc1.640028',
	hevc: 'hvc1.1.6.L120.90',
	av1: 'av01.0.08M.08',
	vp9: 'vp09.00.40.08',
	vp8: 'vp8',
	theora: 'theora',
	aac: 'mp4a.40.2',
	mp3: 'mp4a.6B',
	opus: 'opus',
	vorbis: 'vorbis',
	flac: 'fLaC',
	ac3: 'ac-3',
	eac3: 'ec-3',
	alac: 'alac'
};

/** Browser-native containers and the MIME used to probe them. */
export const NATIVE_CONTAINERS: Record<string, string> = {
	mp4: 'video/mp4',
	m4v: 'video/mp4',
	mov: 'video/mp4',
	webm: 'video/webm',
	ogv: 'video/ogg',
	ogg: 'audio/ogg',
	oga: 'audio/ogg',
	opus: 'audio/ogg',
	mp3: 'audio/mpeg',
	m4a: 'audio/mp4',
	aac: 'audio/aac',
	wav: 'audio/wav',
	flac: 'audio/flac'
};

export type Playback =
	| { mode: 'direct'; audio: number }
	| { mode: 'remux'; video: number; audio: number }
	| { mode: 'remux-video-only'; video: number; audioCodec: string | null }
	| { mode: 'unsupported' };

type CanPlay = (type: string) => string;

function pickAudio(info: MediaInfo, ok: (codec: string) => boolean): number {
	const playable = info.audio.filter((a) => ok(a.codec));
	if (!playable.length) return -1;
	return (playable.find((a) => a.default) ?? playable[0]).index;
}

/** Decides whether a file plays directly, remuxed, video-only, or not at all. */
export function decidePlayback(name: string, info: MediaInfo, canPlay: CanPlay): Playback {
	const video = info.video[0];
	const e = ext(name);
	const native = NATIVE_CONTAINERS[e];
	if (native) {
		const audioIdx = info.audio.length ? (info.audio.find((a) => a.default) ?? info.audio[0]).index : -1;
		const audio = info.audio.find((a) => a.index === audioIdx);
		const codecs = [video?.codec, audio?.codec]
			.filter((c): c is string => !!c)
			.map((c) => CODEC_STRINGS[c])
			.filter(Boolean);
		const mime = video ? native.replace(/^audio\//, 'video/') : native.replace(/^video\//, 'audio/');
		const type = codecs.length ? `${mime}; codecs="${codecs.join(', ')}"` : mime;
		const known = [video?.codec, audio?.codec].filter(Boolean).every((c) => CODEC_STRINGS[c!] || /^pcm_/.test(c!));
		if (known && canPlay(type) !== '') return { mode: 'direct', audio: audioIdx };
	}
	const audioOk = (c: string) => !!CODEC_STRINGS[c] && canPlay(`audio/mp4; codecs="${CODEC_STRINGS[c]}"`) !== '';
	if (!video) {
		const a = pickAudio(info, audioOk);
		return a >= 0 ? { mode: 'remux', video: -1, audio: a } : { mode: 'unsupported' };
	}
	const vs = CODEC_STRINGS[video.codec];
	if (!vs || canPlay(`video/mp4; codecs="${vs}"`) === '') return { mode: 'unsupported' };
	const a = pickAudio(info, audioOk);
	if (a >= 0) return { mode: 'remux', video: video.index, audio: a };
	return { mode: 'remux-video-only', video: video.index, audioCodec: info.audio[0]?.codec ?? null };
}

/** Human codec labels for the audio banner. */
export function codecLabel(c: string | null): string {
	if (!c) return 'unknown';
	const m: Record<string, string> = { dts: 'DTS', truehd: 'TrueHD', ac3: 'AC-3', eac3: 'E-AC-3', wmav2: 'WMA', ape: 'APE' };
	return m[c] ?? c.toUpperCase();
}
