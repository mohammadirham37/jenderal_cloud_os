<script lang="ts">
	import type { Snippet } from 'svelte';
	import { CircleAlert, CircleCheck, Info, TriangleAlert } from '@lucide/svelte';

	let {
		tone = 'info',
		title,
		children
	}: {
		tone?: 'info' | 'success' | 'warning' | 'danger';
		title?: string;
		children?: Snippet;
	} = $props();
	const styles = {
		info: 'bg-info-soft text-info border-info/25',
		success: 'bg-success-soft text-success border-success/25',
		warning: 'bg-warning-soft text-warning border-warning/25',
		danger: 'bg-danger-soft text-danger border-danger/25'
	};
	const icons = { info: Info, success: CircleCheck, warning: TriangleAlert, danger: CircleAlert };
	const Icon = $derived(icons[tone]);
</script>

<div
	class="flex gap-3 rounded-md border px-4 py-3 text-sm {styles[tone]}"
	role={tone === 'danger' ? 'alert' : 'status'}
>
	<Icon class="mt-0.5 size-4 shrink-0" aria-hidden="true" />
	<div class="min-w-0 text-fg">
		{#if title}<p class="font-semibold">{title}</p>{/if}
		{#if children}<div class="[&_a]:link text-fg-muted">{@render children()}</div>{/if}
	</div>
</div>
