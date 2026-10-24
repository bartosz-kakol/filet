// Time bookkeeping for remuxed (non-seekable) streams: the element plays a
// stream that starts at `offset` seconds into the file.

export interface Cue {
	start: number;
	end: number;
	text: string;
}

export type SeekPlan = { kind: 'local'; currentTime: number } | { kind: 'restart'; offset: number };

/** Displayed time = offset + element time. */
export function displayTime(offset: number, currentTime: number): number {
	return offset + currentTime;
}

/**
 * Seeking to T stays local when T is inside a buffered range (element
 * coordinates); otherwise the stream restarts at T.
 */
export function planSeek(t: number, offset: number, buffered: [number, number][]): SeekPlan {
	const local = t - offset;
	if (local >= 0 && buffered.some(([s, e]) => local >= s && local <= e)) return { kind: 'local', currentTime: local };
	return { kind: 'restart', offset: Math.max(0, t) };
}

/** Shifts cues by −offset for a stream starting at offset; drops past cues. */
export function shiftCues(cues: Cue[], offset: number): Cue[] {
	return cues
		.map((c) => ({ start: c.start - offset, end: c.end - offset, text: c.text }))
		.filter((c) => c.end > 0)
		.map((c) => ({ ...c, start: Math.max(0, c.start) }));
}

function parseTs(s: string): number {
	const m = s.trim().match(/^(?:(\d+):)?(\d{1,2}):(\d{2})[.,](\d{1,3})/);
	if (!m) return NaN;
	return (Number(m[1] ?? 0) * 3600 + Number(m[2]) * 60 + Number(m[3])) + Number(m[4].padEnd(3, '0')) / 1000;
}

/** Minimal WebVTT cue parser (timings + text). */
export function parseVtt(text: string): Cue[] {
	const cues: Cue[] = [];
	const blocks = text.replace(/\r\n?/g, '\n').split(/\n{2,}/);
	for (const b of blocks) {
		const lines = b.split('\n');
		const ti = lines.findIndex((l) => l.includes('-->'));
		if (ti < 0) continue;
		const [a, rest] = lines[ti].split('-->');
		const start = parseTs(a);
		const end = parseTs(rest);
		if (Number.isNaN(start) || Number.isNaN(end)) continue;
		cues.push({ start, end, text: lines.slice(ti + 1).join('\n').replace(/\n+$/, '') });
	}
	return cues;
}
