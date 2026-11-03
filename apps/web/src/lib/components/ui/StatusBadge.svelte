<script lang="ts">
	import Badge from './Badge.svelte';
	import { titleCase } from '#lib/format.ts';

	let { status, pending = null }: { status: string; pending?: string | null } = $props();

	type Tone = 'neutral' | 'success' | 'warning' | 'danger' | 'info' | 'accent';
	const tone = $derived.by((): Tone => {
		switch (status) {
			case 'running':
			case 'ready':
			case 'active':
			case 'succeeded':
			case 'success':
			case 'approved':
			case 'online':
			case 'up':
			case 'available':
			case 'resolved':
				return 'success';
			case 'provisioning':
			case 'requested':
			case 'starting':
			case 'stopping':
			case 'queued':
			case 'pending':
			case 'deleting':
			case 'removing':
				return 'info';
			case 'suspended':
			case 'denied':
			case 'degraded':
			case 'warning':
			case 'expired':
			case 'missing':
				return 'warning';
			case 'failed':
			case 'failure':
			case 'dead':
			case 'rejected':
			case 'offline':
			case 'down':
			case 'firing':
			case 'critical':
				return 'danger';
			default:
				return 'neutral';
		}
	});
	const busy = $derived(pending !== null && pending !== undefined);
</script>

<Badge tone={busy ? 'info' : tone} dot>
	{titleCase(status)}{#if busy}<span class="sr-only">, operation in progress</span>…{/if}
</Badge>
