<script lang="ts">
	// Compact "CPU · memory · storage" summary.
	import { bytes, cores } from '#lib/format.ts';

	let { cpu, memory, storage }: { cpu: number; memory: number; storage?: number | null } = $props();
</script>

<span class="text-xs whitespace-nowrap text-fg-muted tabular-nums">
	<span title="CPU limit">{cores(cpu)}</span>
	<span aria-hidden="true">·</span>
	<span title="Memory limit">{bytes(memory)}</span>
	{#if storage !== undefined && storage !== null}
		<span aria-hidden="true">·</span>
		<span title="Storage">{bytes(storage)}</span>
	{/if}
</span>
