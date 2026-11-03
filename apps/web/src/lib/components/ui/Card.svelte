<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		title,
		description,
		actions,
		children,
		padded = true,
		class: klass = ''
	}: {
		title?: string;
		description?: string;
		actions?: Snippet;
		children: Snippet;
		padded?: boolean;
		class?: string;
	} = $props();
</script>

<section class="card {klass}">
	{#if title || actions}
		<header
			class="flex flex-wrap items-start justify-between gap-3 border-b border-border px-5 py-3.5"
		>
			<div>
				{#if title}<h2 class="text-sm font-semibold text-fg">{title}</h2>{/if}
				{#if description}<p class="mt-0.5 text-xs text-fg-muted">{description}</p>{/if}
			</div>
			{#if actions}<div class="flex items-center gap-2">{@render actions()}</div>{/if}
		</header>
	{/if}
	<div class={padded ? 'p-5' : ''}>{@render children()}</div>
</section>
