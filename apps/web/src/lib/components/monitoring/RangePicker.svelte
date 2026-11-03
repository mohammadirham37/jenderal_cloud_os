<script lang="ts">
	import type { HistoryRange } from '#lib/api/types-admin.ts';

	let { value = $bindable('1h'), label = 'Time range' }: { value?: HistoryRange; label?: string } =
		$props();
	const ranges: { id: HistoryRange; label: string }[] = [
		{ id: '1h', label: '1 hour' },
		{ id: '6h', label: '6 hours' },
		{ id: '24h', label: '24 hours' },
		{ id: '7d', label: '7 days' },
		{ id: '14d', label: '14 days' }
	];
</script>

<div
	class="inline-flex overflow-hidden rounded-md border border-border-strong"
	role="radiogroup"
	aria-label={label}
>
	{#each ranges as r (r.id)}
		<button
			type="button"
			role="radio"
			aria-checked={value === r.id}
			class="px-2.5 py-1.5 text-xs font-medium transition-colors {value === r.id
				? 'bg-accent text-accent-fg'
				: 'bg-panel text-fg-muted hover:bg-panel-2 hover:text-fg'}"
			onclick={() => (value = r.id)}>{r.id}<span class="sr-only"> ({r.label})</span></button
		>
	{/each}
</div>
