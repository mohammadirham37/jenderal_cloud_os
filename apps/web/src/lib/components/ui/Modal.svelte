<script lang="ts">
	import type { Snippet } from 'svelte';
	import { X } from '@lucide/svelte';

	let {
		open = $bindable(false),
		title,
		description,
		size = 'md',
		children,
		footer,
		onclose
	}: {
		open?: boolean;
		title: string;
		description?: string;
		size?: 'sm' | 'md' | 'lg' | 'xl';
		children: Snippet;
		footer?: Snippet;
		onclose?: () => void;
	} = $props();

	let dialog = $state<HTMLDialogElement>();
	const uid = $props.id();
	const widths = { sm: 'max-w-sm', md: 'max-w-lg', lg: 'max-w-2xl', xl: 'max-w-4xl' };

	$effect(() => {
		if (!dialog) return;
		if (open && !dialog.open) dialog.showModal();
		if (!open && dialog.open) dialog.close();
	});

	function close() {
		open = false;
		onclose?.();
	}
</script>

<dialog
	bind:this={dialog}
	class="m-auto w-[calc(100%-2rem)] {widths[
		size
	]} rounded-lg border border-border bg-panel p-0 text-fg shadow-xl backdrop:bg-black/50"
	aria-labelledby="{uid}-title"
	oncancel={(e) => {
		e.preventDefault();
		close();
	}}
>
	{#if open}
		<div class="flex items-start justify-between gap-4 border-b border-border px-5 py-4">
			<div>
				<h2 id="{uid}-title" class="text-base font-semibold">{title}</h2>
				{#if description}<p class="mt-1 text-sm text-fg-muted">{description}</p>{/if}
			</div>
			<button
				type="button"
				class="rounded p-1 text-fg-muted hover:bg-panel-2"
				onclick={close}
				aria-label="Close dialog"
			>
				<X class="size-4" />
			</button>
		</div>
		<div class="max-h-[70vh] overflow-y-auto px-5 py-4">{@render children()}</div>
		{#if footer}
			<div class="flex justify-end gap-2 border-t border-border bg-panel-2/50 px-5 py-3">
				{@render footer()}
			</div>
		{/if}
	{/if}
</dialog>
