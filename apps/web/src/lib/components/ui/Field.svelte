<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		label,
		id,
		error,
		hint,
		required = false,
		children
	}: {
		label: string;
		id: string;
		error?: string;
		hint?: string;
		required?: boolean;
		children: Snippet;
	} = $props();
</script>

<div>
	<label for={id} class="label">
		{label}{#if required}<span class="text-danger" aria-hidden="true"> *</span>{/if}
	</label>
	{@render children()}
	{#if error}
		<p id="{id}-error" class="mt-1 text-xs text-danger" role="alert">{error}</p>
	{:else if hint}
		<p id="{id}-hint" class="mt-1 text-xs text-fg-muted">{hint}</p>
	{/if}
</div>
