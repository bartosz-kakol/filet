import { describe, expect, it } from 'vitest';
import type { MediaInfo } from '$lib/api/types';
import { parseCSV } from './csv';
import { decidePlayback } from './media';
import { displayTime, parseVtt, planSeek, shiftCues } from './remux';
import { looksLikeText } from './sniff';

function info(video: string | null, audio: { codec: string; default?: boolean }[]): MediaInfo {
	return {
		container: '',
		duration: 100,
		bitrate: 0,
		size: 0,
		tags: {},
		video: video ? [{ index: 0, codec: video, width: 1, height: 1, fps: 25, hdr: null, rotation: 0, default: true }] : [],
		audio: audio.map((a, i) => ({ index: i + 1, codec: a.codec, channels: 2, sampleRate: 48000, language: null, title: null, default: !!a.default })),
		subtitles: [],
		chapters: [],
		hasCoverArt: false,
		sidecarSubtitles: []
	};
}

// A Chromium-like codec table.
const chromeLike = (t: string) => (/avc1|mp4a\.40\.2|opus|fLaC|mp4a\.6B|vp09|av01/.test(t) || /^audio\/(mpeg|wav|flac)$/.test(t) ? 'probably' : '');

describe('playback decision', () => {
	it('direct play for native containers and codecs', () => {
		expect(decidePlayback('a.mp4', info('h264', [{ codec: 'aac' }]), chromeLike)).toEqual({ mode: 'direct', audio: 1 });
		expect(decidePlayback('a.mp3', info(null, [{ codec: 'mp3' }]), chromeLike).mode).toBe('direct');
	});
	it('remux for non-native containers, picking the first playable default audio', () => {
		const i = info('h264', [{ codec: 'dts', default: true }, { codec: 'aac' }, { codec: 'opus', default: true }]);
		expect(decidePlayback('a.mkv', i, chromeLike)).toEqual({ mode: 'remux', video: 0, audio: 3 });
		expect(decidePlayback('a.mkv', info('h264', [{ codec: 'dts' }, { codec: 'aac' }]), chromeLike)).toEqual({ mode: 'remux', video: 0, audio: 2 });
	});
	it('video-only remux with a banner when no audio is playable', () => {
		expect(decidePlayback('a.mkv', info('h264', [{ codec: 'dts' }]), chromeLike)).toEqual({ mode: 'remux-video-only', video: 0, audioCodec: 'dts' });
	});
	it('unsupported codecs are never transcoded', () => {
		expect(decidePlayback('a.avi', info('mpeg4', [{ codec: 'mp3' }]), chromeLike).mode).toBe('unsupported');
		expect(decidePlayback('a.wma', info(null, [{ codec: 'wmav2' }]), chromeLike).mode).toBe('unsupported');
		expect(decidePlayback('a.mkv', info('hevc', [{ codec: 'aac' }]), chromeLike).mode).toBe('unsupported');
	});
	it('audio-only remux', () => {
		expect(decidePlayback('a.mka', info(null, [{ codec: 'aac' }]), chromeLike)).toEqual({ mode: 'remux', video: -1, audio: 1 });
	});
});

describe('remux time arithmetic', () => {
	it('display time and seeking', () => {
		expect(displayTime(30, 12.5)).toBe(42.5);
		expect(planSeek(40, 30, [[0, 20]])).toEqual({ kind: 'local', currentTime: 10 });
		expect(planSeek(55, 30, [[0, 20]])).toEqual({ kind: 'restart', offset: 55 });
		expect(planSeek(10, 30, [[0, 20]])).toEqual({ kind: 'restart', offset: 10 });
	});
	it('shifts cues and drops past ones', () => {
		const cues = parseVtt('WEBVTT\n\n00:00:05.000 --> 00:00:08.000\nA\n\n00:01:00.500 --> 00:01:02.000\nB\nline2\n\n01:00:00.000 --> 01:00:01.000\nC\n');
		expect(cues.map((c) => c.text)).toEqual(['A', 'B\nline2', 'C']);
		const s = shiftCues(cues, 59);
		expect(s).toEqual([
			{ start: 1.5, end: 3, text: 'B\nline2' },
			{ start: 3541, end: 3542, text: 'C' }
		]);
		expect(shiftCues([{ start: 58, end: 61, text: 'x' }], 59)).toEqual([{ start: 0, end: 2, text: 'x' }]);
	});
});

describe('CSV parser', () => {
	it('quoted commas, escaped quotes, CRLF', () => {
		const { rows } = parseCSV('a,b,c\r\n1,"x, y",3\r\n"he said ""hi""",,\r\n"multi\nline",z,\n');
		expect(rows).toEqual([
			['a', 'b', 'c'],
			['1', 'x, y', '3'],
			['he said "hi"', '', ''],
			['multi\nline', 'z', '']
		]);
		expect(parseCSV('a\tb\n1\t2', '\t').rows).toEqual([['a', 'b'], ['1', '2']]);
		expect(parseCSV('1\n2\n3\n', ',', 2)).toEqual({ rows: [['1'], ['2']], truncated: true });
	});
});

describe('text sniffing', () => {
	it('accepts UTF-8 (even split at the end) and rejects NUL', () => {
		const t = new TextEncoder().encode('zażółć gęślą');
		expect(looksLikeText(t)).toBe(true);
		expect(looksLikeText(t.subarray(0, t.length - 1))).toBe(true);
		expect(looksLikeText(new Uint8Array([0x41, 0x00, 0x42]))).toBe(false);
		expect(looksLikeText(new Uint8Array([0xff, 0xfe, 0xfd, 0x41, 0x42]))).toBe(false);
	});
});
