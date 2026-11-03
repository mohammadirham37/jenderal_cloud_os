// Pure helpers for workspace-relative paths ("" is the workspace home).
import type { FileEntry } from '#lib/api/types.ts';

/** Normalises a user/URL supplied relative path: no leading/trailing or double slashes. */
export function normalize(p: string | null | undefined): string {
	return (p ?? '')
		.split('/')
		.filter((s) => s !== '' && s !== '.')
		.join('/');
}

export function join(dir: string, name: string): string {
	return normalize(`${dir}/${name}`);
}

export function parent(p: string): string {
	const parts = normalize(p).split('/');
	parts.pop();
	return parts.join('/');
}

export function basename(p: string): string {
	const parts = normalize(p).split('/');
	return parts[parts.length - 1] ?? '';
}

/** Breadcrumb segments: [{ name: 'Home', path: '' }, { name: 'src', path: 'src' }, …]. */
export function segments(p: string): { name: string; path: string }[] {
	const out = [{ name: 'Home', path: '' }];
	let acc = '';
	for (const s of normalize(p).split('/')) {
		if (!s) continue;
		acc = acc ? `${acc}/${s}` : s;
		out.push({ name: s, path: acc });
	}
	return out;
}

/** True when `p` equals `ancestor` or lies inside it ("" contains everything). */
export function within(p: string, ancestor: string): boolean {
	return ancestor === '' || p === ancestor || p.startsWith(`${ancestor}/`);
}

/** Validates a single file/folder name typed by the user; returns an error or null. */
export function nameError(name: string): string | null {
	const n = name.trim();
	if (!n) return 'Enter a name.';
	if (n === '.' || n === '..') return 'This name is not allowed.';
	if (n.includes('/') || n.includes('\\')) return 'Use a name without slashes.';
	if (n.length > 255) return 'The name is too long (maximum 255 characters).';
	return null;
}

/** Directories first, then by name (natural, case-insensitive). */
export function sortEntries(entries: FileEntry[]): FileEntry[] {
	return [...entries].sort((a, b) => {
		const ad = a.type === 'dir' ? 0 : 1;
		const bd = b.type === 'dir' ? 0 : 1;
		if (ad !== bd) return ad - bd;
		return a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: 'base' });
	});
}

export function typeLabel(e: FileEntry): string {
	if (e.type === 'dir') return 'Folder';
	if (e.type === 'symlink') return 'Link';
	if (e.type === 'other') return 'Special';
	const dot = e.name.lastIndexOf('.');
	if (dot > 0 && dot < e.name.length - 1) return `${e.name.slice(dot + 1).toUpperCase()} file`;
	return 'File';
}
