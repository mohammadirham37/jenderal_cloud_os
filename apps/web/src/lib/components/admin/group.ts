/** Groups items by a key, preserving first-seen key order. */
export function groupBy<T>(items: readonly T[], key: (t: T) => string): [string, T[]][] {
	const out: Record<string, T[]> = {};
	for (const it of items) (out[key(it)] ??= []).push(it);
	return Object.entries(out);
}
