<script lang="ts">
	// Horizontal usage meter. `value` and `max` are in the same unit; when the
	// value is unknown the meter says so instead of showing a fake number.
	let {
		value,
		max,
		label,
		display
	}: { value: number | null | undefined; max: number; label: string; display?: string } = $props();
	const pct = $derived(
		value === null || value === undefined || max <= 0 ? null : Math.min(100, (value / max) * 100)
	);
	const color = $derived(
		pct === null ? 'bg-border' : pct >= 90 ? 'bg-danger' : pct >= 75 ? 'bg-warning' : 'bg-accent'
	);
</script>

<div>
	<div class="mb-1 flex justify-between text-xs">
		<span class="text-fg-muted">{label}</span>
		<span class="font-medium text-fg tabular-nums"
			>{pct === null ? 'Unavailable' : (display ?? `${pct.toFixed(0)}%`)}</span
		>
	</div>
	<div
		class="h-2 overflow-hidden rounded-full bg-panel-2"
		role="meter"
		aria-label={label}
		aria-valuemin={0}
		aria-valuemax={100}
		aria-valuenow={pct ?? undefined}
	>
		<div class="h-full rounded-full transition-all {color}" style="width: {pct ?? 0}%"></div>
	</div>
</div>
