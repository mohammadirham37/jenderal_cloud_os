<script lang="ts">
	import { CircleAlert, CircleCheck, Info, TriangleAlert, X } from '@lucide/svelte';
	import { toast } from '#lib/stores/toast.svelte.ts';

	const icons = { success: CircleCheck, error: CircleAlert, info: Info, warning: TriangleAlert };
	const colors = {
		success: 'text-success',
		error: 'text-danger',
		info: 'text-info',
		warning: 'text-warning'
	};
</script>

<div
	class="pointer-events-none fixed right-4 bottom-4 z-50 flex w-full max-w-sm flex-col gap-2"
	aria-live="polite"
>
	{#each toast.items as t (t.id)}
		{@const Icon = icons[t.kind]}
		<div
			class="card pointer-events-auto flex gap-3 p-3.5 shadow-lg"
			role={t.kind === 'error' ? 'alert' : 'status'}
		>
			<Icon class="mt-0.5 size-4 shrink-0 {colors[t.kind]}" aria-hidden="true" />
			<div class="min-w-0 flex-1">
				<p class="text-sm font-medium text-fg">{t.title}</p>
				{#if t.message}<p class="mt-0.5 text-sm break-words text-fg-muted">{t.message}</p>{/if}
			</div>
			<button
				class="text-fg-subtle hover:text-fg"
				onclick={() => toast.dismiss(t.id)}
				aria-label="Dismiss notification"
			>
				<X class="size-4" />
			</button>
		</div>
	{/each}
</div>
