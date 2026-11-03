<script lang="ts">
	import type { Component, Snippet } from 'svelte';

	let {
		label,
		value,
		sub,
		icon,
		children
	}: { label: string; value: string; sub?: string; icon?: Component; children?: Snippet } =
		$props();
	const Icon = $derived(icon);
</script>

<div class="card p-4">
	<div class="flex items-center justify-between">
		<p class="text-xs font-medium tracking-wide text-fg-muted uppercase">{label}</p>
		{#if Icon}<Icon class="size-4 text-fg-subtle" aria-hidden="true" />{/if}
	</div>
	<p class="mt-2 text-2xl font-semibold text-fg tabular-nums">{value}</p>
	{#if sub}<p class="mt-0.5 text-xs text-fg-muted">{sub}</p>{/if}
	{#if children}<div class="mt-3">{@render children()}</div>{/if}
</div>
