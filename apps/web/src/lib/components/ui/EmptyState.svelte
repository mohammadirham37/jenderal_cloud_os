<script lang="ts">
	import type { Snippet, Component } from 'svelte';
	import { Inbox } from '@lucide/svelte';

	let {
		title,
		description,
		icon = Inbox,
		action
	}: { title: string; description?: string; icon?: Component; action?: Snippet } = $props();
	const Icon = $derived(icon);
</script>

<div class="flex flex-col items-center justify-center px-6 py-12 text-center">
	<div class="mb-3 rounded-full bg-panel-2 p-3 text-fg-subtle">
		<Icon class="size-6" aria-hidden="true" />
	</div>
	<h3 class="text-sm font-semibold text-fg">{title}</h3>
	{#if description}<p class="mt-1 max-w-md text-sm text-fg-muted">{description}</p>{/if}
	{#if action}<div class="mt-4">{@render action()}</div>{/if}
</div>
