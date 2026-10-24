import { execFileSync, spawnSync } from 'node:child_process';
import { readFileSync } from 'node:fs';

export const hasFFmpeg = spawnSync('ffmpeg', ['-version']).status === 0;

/** Runs ffmpeg with the given arguments (output path last). */
export function ffmpeg(...args: string[]) {
	execFileSync('ffmpeg', ['-y', '-loglevel', 'error', ...args]);
}

export function makeVideo(out: string, opts: { container?: 'mp4' | 'mkv'; audio?: 'aac' | 'dts'; seconds?: number } = {}) {
	const s = String(opts.seconds ?? 6);
	const audioArgs =
		opts.audio === 'dts' ? ['-c:a', 'dca', '-strict', '-2', '-ar', '48000', '-ac', '2'] : ['-c:a', 'aac'];
	ffmpeg(
		'-f', 'lavfi', '-i', `testsrc2=size=320x240:rate=25:duration=${s}`,
		'-f', 'lavfi', '-i', `sine=frequency=440:duration=${s}`,
		'-c:v', 'libx264', '-pix_fmt', 'yuv420p', '-g', '25', ...audioArgs, '-shortest', out
	);
}

export function makeJpeg(out: string, hue = 0, size = '640x480') {
	ffmpeg('-f', 'lavfi', '-i', `testsrc=size=${size}:rate=1`, '-vf', `hue=h=${hue}`, '-frames:v', '1', out);
	return readFileSync(out);
}

// --- EXIF ---

type Value = { tag: number; type: 2 | 4 | 5; data: Buffer; count: number };

const ascii = (tag: number, s: string): Value => ({ tag, type: 2, data: Buffer.from(s + '\0'), count: s.length + 1 });
const long = (tag: number, n: number): Value => {
	const b = Buffer.alloc(4);
	b.writeUInt32BE(n);
	return { tag, type: 4, data: b, count: 1 };
};
const rationals = (tag: number, vals: [number, number][]): Value => {
	const b = Buffer.alloc(8 * vals.length);
	vals.forEach(([n, d], i) => {
		b.writeUInt32BE(n, i * 8);
		b.writeUInt32BE(d, i * 8 + 4);
	});
	return { tag, type: 5, data: b, count: vals.length };
};

/** Serialises one IFD at `offset` (TIFF-relative); returns its bytes. */
function ifd(values: Value[], offset: number): Buffer {
	values.sort((a, b) => a.tag - b.tag);
	const head = 2 + values.length * 12 + 4;
	let dataOff = offset + head;
	const entries = Buffer.alloc(head);
	const blobs: Buffer[] = [];
	entries.writeUInt16BE(values.length, 0);
	values.forEach((v, i) => {
		const e = 2 + i * 12;
		entries.writeUInt16BE(v.tag, e);
		entries.writeUInt16BE(v.type, e + 2);
		entries.writeUInt32BE(v.count, e + 4);
		if (v.data.length <= 4) v.data.copy(entries, e + 8);
		else {
			entries.writeUInt32BE(dataOff, e + 8);
			const padded = v.data.length % 2 ? Buffer.concat([v.data, Buffer.alloc(1)]) : v.data;
			blobs.push(padded);
			dataOff += padded.length;
		}
	});
	return Buffer.concat([entries, ...blobs]);
}

const dms = (deg: number): [number, number][] => {
	const d = Math.floor(deg);
	const m = Math.floor((deg - d) * 60);
	const s = Math.round(((deg - d) * 60 - m) * 60 * 100);
	return [[d, 1], [m, 1], [s, 100]];
};

/** Inserts an EXIF APP1 segment with camera and GPS data into a JPEG. */
export function withExif(jpeg: Buffer, e: { make: string; model: string; lat: number; lon: number }): Buffer {
	const tiffHeader = Buffer.from([0x4d, 0x4d, 0x00, 0x2a, 0x00, 0x00, 0x00, 0x08]);
	// IFD0 at 8; its size depends on the GPS pointer value, which is fixed-size.
	const ifd0Values = [ascii(0x010f, e.make), ascii(0x0110, e.model), long(0x8825, 0)];
	const ifd0Len = ifd(ifd0Values.map((v) => ({ ...v })), 8).length;
	const gpsOff = 8 + ifd0Len;
	ifd0Values[2] = long(0x8825, gpsOff);
	const ifd0 = ifd(ifd0Values, 8);
	const gps = ifd(
		[ascii(0x0001, 'N'), rationals(0x0002, dms(e.lat)), ascii(0x0003, 'E'), rationals(0x0004, dms(e.lon))],
		gpsOff
	);
	const tiff = Buffer.concat([tiffHeader, ifd0, gps]);
	const payload = Buffer.concat([Buffer.from('Exif\0\0', 'binary'), tiff]);
	const app1 = Buffer.alloc(4);
	app1.writeUInt16BE(0xffe1, 0);
	app1.writeUInt16BE(payload.length + 2, 2);
	return Buffer.concat([jpeg.subarray(0, 2), app1, payload, jpeg.subarray(2)]);
}

/** A minimal one-page PDF. */
export function minimalPdf(): Buffer {
	const objs = [
		'<< /Type /Catalog /Pages 2 0 R >>',
		'<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
		'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
		'<< /Length 44 >>\nstream\nBT /F1 24 Tf 40 100 Td (Hello PDF) Tj ET\nendstream',
		'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'
	];
	let out = '%PDF-1.4\n';
	const offs: number[] = [];
	objs.forEach((o, i) => {
		offs.push(out.length);
		out += `${i + 1} 0 obj\n${o}\nendobj\n`;
	});
	const xref = out.length;
	out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n`;
	for (const o of offs) out += `${String(o).padStart(10, '0')} 00000 n \n`;
	out += `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
	return Buffer.from(out, 'binary');
}
