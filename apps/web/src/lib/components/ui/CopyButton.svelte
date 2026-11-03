<script lang="ts">
	import { Check, Copy } from '@lucide/svelte';

	let { text, label = 'Copy' }: { text: string; label?: string } = $props();
	let copied = $state(false);

	async function copy() {
		try {
			await navigator.clipboard.writeText(text);
			copied = true;
			setTimeout(() => (copied = false), 1500);
		} catch {
			copied = false;
		}
	}
</script>

<button
	type="button"
	class="inline-flex items-center gap-1 rounded p-1 text-fg-muted hover:bg-panel-2 hover:text-fg"
	onclick={copy}
	aria-label={label}
	title={label}
>
	{#if copied}<Check class="size-4 text-success" />{:else}<Copy class="size-4" />{/if}
</button>
