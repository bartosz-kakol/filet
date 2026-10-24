const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];

/** Decimal byte sizes ("2.4 MB"). */
export function formatBytes(n: number | null | undefined): string {
	if (n === null || n === undefined || !Number.isFinite(n)) return '—';
	if (n < 1000) return `${n} B`;
	let i = 0;
	let v = n;
	while (v >= 1000 && i < UNITS.length - 1) {
		v /= 1000;
		i++;
	}
	return `${v >= 100 ? v.toFixed(0) : v >= 10 ? v.toFixed(1) : v.toFixed(2).replace(/0$/, '')} ${UNITS[i]}`;
}

export function formatSpeed(bps: number): string {
	return `${formatBytes(Math.round(bps))}/s`;
}

const dateFmt = new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short', year: 'numeric' });
const dateTimeFmt = new Intl.DateTimeFormat(undefined, {
	day: 'numeric',
	month: 'short',
	year: 'numeric',
	hour: '2-digit',
	minute: '2-digit'
});

export function formatDate(ms: number | null | undefined): string {
	return ms ? dateFmt.format(ms) : '—';
}

export function formatDateTime(ms: number | null | undefined): string {
	return ms ? dateTimeFmt.format(ms) : '—';
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });

/** "in 3 days", "14 minutes ago". */
export function formatRelative(ms: number | null | undefined, now = Date.now()): string {
	if (!ms) return '—';
	const diff = ms - now;
	const abs = Math.abs(diff);
	const table: [number, Intl.RelativeTimeFormatUnit][] = [
		[60_000, 'second'],
		[3_600_000, 'minute'],
		[86_400_000, 'hour'],
		[2_592_000_000, 'day'],
		[31_536_000_000, 'month'],
		[Infinity, 'year']
	];
	const div: Record<string, number> = {
		second: 1000,
		minute: 60_000,
		hour: 3_600_000,
		day: 86_400_000,
		month: 2_592_000_000,
		year: 31_536_000_000
	};
	for (const [limit, unit] of table) {
		if (abs < limit) return rtf.format(Math.round(diff / div[unit]), unit);
	}
	return '—';
}

/** h:mm:ss or m:ss. */
export function formatDuration(sec: number): string {
	if (!Number.isFinite(sec) || sec < 0) return '0:00';
	const s = Math.floor(sec % 60);
	const m = Math.floor((sec / 60) % 60);
	const h = Math.floor(sec / 3600);
	const pad = (n: number) => String(n).padStart(2, '0');
	return h > 0 ? `${h}:${pad(m)}:${pad(s)}` : `${m}:${pad(s)}`;
}

/** "2 y 181 d" from hours. */
export function formatPowerOn(hours: number | null | undefined): string {
	if (hours === null || hours === undefined) return '—';
	const days = Math.floor(hours / 24);
	const y = Math.floor(days / 365);
	const d = days % 365;
	return y > 0 ? `${y} y ${d} d` : `${d} d ${hours % 24} h`;
}

export function formatEta(sec: number): string {
	if (!Number.isFinite(sec) || sec <= 0) return '';
	if (sec < 60) return `${Math.ceil(sec)} s left`;
	if (sec < 3600) return `${Math.floor(sec / 60)}:${String(Math.floor(sec % 60)).padStart(2, '0')} left`;
	return `${Math.floor(sec / 3600)} h ${Math.floor((sec % 3600) / 60)} min left`;
}

/** Leftover badge text: hidden for 0, digits up to 9, "9+" above. */
export function badgeText(n: number): string | null {
	if (n <= 0) return null;
	return n > 9 ? '9+' : String(n);
}

export function formatCount(n: number): string {
	return n.toLocaleString();
}
