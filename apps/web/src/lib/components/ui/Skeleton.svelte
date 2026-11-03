<script lang="ts">
	let { lines = 3, class: klass = '' }: { lines?: number; class?: string } = $props();
</script>

<div class="space-y-3 {klass}" aria-busy="true" aria-label="Loading">
	{#each Array.from({ length: lines }, (_, i) => i) as i (i)}
		<div class="h-4 animate-pulse rounded bg-panel-2" style="width: {90 - ((i * 17) % 40)}%"></div>
	{/each}
</div>
