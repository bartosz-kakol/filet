export interface ExifSummary {
	rows: [string, string | number | null | undefined][];
	gps: { lat: number; lon: number } | null;
}

type Tags = Record<string, unknown>;

const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : null);

function shutter(v: unknown): string | null {
	const n = num(v);
	if (n === null) return null;
	return n >= 1 ? `${n} s` : `1/${Math.round(1 / n)} s`;
}

/** Picks the fields shown in the info panel from exifr output. */
export function summarizeExif(t: Tags): ExifSummary {
	const w = num(t.ExifImageWidth) ?? num(t.ImageWidth);
	const h = num(t.ExifImageHeight) ?? num(t.ImageHeight);
	const taken = t.DateTimeOriginal instanceof Date ? t.DateTimeOriginal.toLocaleString() : (t.DateTimeOriginal as string | undefined);
	const lat = num(t.latitude);
	const lon = num(t.longitude);
	const focal = num(t.FocalLength);
	const focal35 = num(t.FocalLengthIn35mmFormat);
	return {
		rows: [
			['Dimensions', w && h ? `${w}×${h} (${((w * h) / 1e6).toFixed(1)} MP)` : null],
			['Camera', [t.Make, t.Model].filter(Boolean).join(' ') || null],
			['Lens', (t.LensModel as string) ?? null],
			['Focal length', focal ? `${focal} mm${focal35 ? ` (${focal35} mm equiv.)` : ''}` : null],
			['Aperture', num(t.FNumber) ? `f/${t.FNumber}` : null],
			['Shutter', shutter(t.ExposureTime)],
			['ISO', num(t.ISO)],
			['Exposure comp.', num(t.ExposureCompensation) !== null ? `${t.ExposureCompensation} EV` : null],
			['Flash', (t.Flash as string) ?? null],
			['White balance', (t.WhiteBalance as string) ?? null],
			['Taken', taken ? `${taken}${t.OffsetTimeOriginal ? ' ' + t.OffsetTimeOriginal : ''}` : null],
			['Orientation', (t.Orientation as string) ?? null],
			['Software', (t.Software as string) ?? null],
			['Colour space', (t.ColorSpace as string) ?? null],
			['Latitude', lat !== null ? lat.toFixed(6) : null],
			['Longitude', lon !== null ? lon.toFixed(6) : null],
			['Altitude', num(t.GPSAltitude) !== null ? `${(t.GPSAltitude as number).toFixed(1)} m` : null],
			['Caption', (t.Caption as string) ?? (t.ImageDescription as string) ?? null],
			['Keywords', Array.isArray(t.Keywords) ? t.Keywords.join(', ') : ((t.Keywords as string) ?? null)],
			['Copyright', (t.Copyright as string) ?? null],
			['Rating', num(t.Rating)]
		],
		gps: lat !== null && lon !== null ? { lat, lon } : null
	};
}
