export interface KV {
	key: string;
	value: string;
}

export function toRows(rec: Record<string, string> | null | undefined): KV[] {
	return Object.entries(rec ?? {}).map(([key, value]) => ({ key, value }));
}

export function toRecord(rows: KV[]): Record<string, string> {
	const out: Record<string, string> = {};
	for (const r of rows) {
		const k = r.key.trim();
		if (k) out[k] = r.value;
	}
	return out;
}
