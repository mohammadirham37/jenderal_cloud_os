<script lang="ts">
	// Tiny per-core CPU usage bars.
	let { cores }: { cores: number[] } = $props();
	const clamp = (v: number) => Math.min(100, Math.max(0, v));
</script>

<div
	class="flex h-8 items-stretch gap-px"
	role="img"
	aria-label="Per-core CPU usage: {cores.map((c, i) => `core ${i} ${c.toFixed(0)}%`).join(', ')}"
>
	{#each cores as c, i (i)}
		<div
			class="flex min-w-0.5 flex-1 flex-col justify-end rounded-sm bg-panel-2"
			title="Core {i}: {c.toFixed(0)}%"
		>
			<div
				class="w-full rounded-sm {c >= 90 ? 'bg-danger' : c >= 75 ? 'bg-warning' : 'bg-accent'}"
				style="height: {clamp(c)}%"
			></div>
		</div>
	{/each}
</div>
