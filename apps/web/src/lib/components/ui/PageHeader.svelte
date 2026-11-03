<script lang="ts">
	import type { Snippet } from 'svelte';
	import { appConfig } from '#lib/stores/config.svelte.ts';

	let {
		title,
		description,
		actions,
		meta
	}: { title: string; description?: string; actions?: Snippet; meta?: Snippet } = $props();
</script>

<svelte:head><title>{title} · {appConfig.name}</title></svelte:head>

<div class="mb-6 flex flex-wrap items-start justify-between gap-4">
	<div class="min-w-0">
		<h1 class="truncate text-xl font-semibold tracking-tight text-fg">{title}</h1>
		{#if description}<p class="mt-1 text-sm text-fg-muted">{description}</p>{/if}
		{#if meta}<div class="mt-2 flex flex-wrap items-center gap-2 text-sm text-fg-muted">
				{@render meta()}
			</div>{/if}
	</div>
	{#if actions}<div class="flex flex-wrap items-center gap-2">{@render actions()}</div>{/if}
</div>
