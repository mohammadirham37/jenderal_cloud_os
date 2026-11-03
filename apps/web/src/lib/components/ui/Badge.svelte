<script lang="ts">
	import type { Snippet } from 'svelte';

	type Tone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'accent';
	let {
		tone = 'neutral',
		dot = false,
		children
	}: { tone?: Tone; dot?: boolean; children: Snippet } = $props();

	const tones: Record<Tone, string> = {
		neutral: 'bg-panel-2 text-fg-muted ring-border',
		success: 'bg-success-soft text-success ring-success/20',
		warning: 'bg-warning-soft text-warning ring-warning/20',
		danger: 'bg-danger-soft text-danger ring-danger/20',
		info: 'bg-info-soft text-info ring-info/20',
		accent: 'bg-accent-soft text-accent ring-accent/20'
	};
</script>

<span
	class="inline-flex items-center gap-1.5 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset {tones[
		tone
	]}"
>
	{#if dot}<span class="size-1.5 rounded-full bg-current" aria-hidden="true"></span>{/if}
	{@render children()}
</span>
