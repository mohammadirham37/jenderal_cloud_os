// Formatting helpers. Times are stored in UTC by the server and shown in the
// browser's local time zone.

const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];

export function bytes(n: number | null | undefined, digits = 1): string {
	if (n === null || n === undefined || Number.isNaN(n)) return '—';
	if (n < 1024) return `${Math.round(n)} B`;
	let i = 0;
	let v = n;
	while (v >= 1024 && i < units.length - 1) {
		v /= 1024;
		i++;
	}
	return `${v.toFixed(v >= 100 ? 0 : digits)} ${units[i]}`;
}

export function rate(n: number | null | undefined): string {
	if (n === null || n === undefined) return '—';
	return `${bytes(n)}/s`;
}

export function cores(millis: number | null | undefined): string {
	if (millis === null || millis === undefined) return '—';
	const c = millis / 1000;
	return `${Number.isInteger(c) ? c : c.toFixed(2).replace(/0+$/, '')} ${c === 1 ? 'core' : 'cores'}`;
}

export function percent(n: number | null | undefined, digits = 0): string {
	if (n === null || n === undefined || Number.isNaN(n)) return '—';
	return `${n.toFixed(digits)}%`;
}

const dtf = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });
const df = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium' });

export function dateTime(s: string | null | undefined): string {
	if (!s) return '—';
	const d = new Date(s);
	return Number.isNaN(d.getTime()) ? '—' : dtf.format(d);
}

export function date(s: string | null | undefined): string {
	if (!s) return '—';
	const d = new Date(s);
	return Number.isNaN(d.getTime()) ? '—' : df.format(d);
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });

export function relative(s: string | null | undefined, now = Date.now()): string {
	if (!s) return '—';
	const t = new Date(s).getTime();
	if (Number.isNaN(t)) return '—';
	const diff = (t - now) / 1000;
	const abs = Math.abs(diff);
	if (abs < 45) return rtf.format(Math.round(diff), 'second');
	if (abs < 2700) return rtf.format(Math.round(diff / 60), 'minute');
	if (abs < 64800) return rtf.format(Math.round(diff / 3600), 'hour');
	if (abs < 2592000) return rtf.format(Math.round(diff / 86400), 'day');
	return date(s);
}

export function duration(seconds: number | null | undefined): string {
	if (seconds === null || seconds === undefined) return '—';
	const d = Math.floor(seconds / 86400);
	const h = Math.floor((seconds % 86400) / 3600);
	const m = Math.floor((seconds % 3600) / 60);
	if (d > 0) return `${d}d ${h}h`;
	if (h > 0) return `${h}h ${m}m`;
	return `${m}m`;
}

/** Converts GiB/MiB input to bytes. */
export const GiB = 1024 ** 3;
export const MiB = 1024 ** 2;

export function titleCase(s: string): string {
	return s.replace(/[_-]+/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
}
