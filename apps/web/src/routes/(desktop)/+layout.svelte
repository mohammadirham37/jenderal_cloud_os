<script lang="ts">
	import { goto } from '$app/navigation';
	import { onMount } from 'svelte';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();
	let ready = $state(false);

	onMount(async () => {
		// A desktop must never load inside one of its own windows.
		if (window.self !== window.top) {
			await goto('/dashboard', { replace: true });
			return;
		}
		const me = auth.me ?? (await auth.load());
		if (!me) {
			await goto('/login', { replace: true });
			return;
		}
		const gate = auth.gate();
		if (gate) {
			await goto(gate, { replace: true });
			return;
		}
		ready = true;
	});
</script>

{#if ready}
	{@render children()}
{:else}
	<div class="fixed inset-0 flex items-center justify-center bg-ink">
		<span
			class="size-8 animate-spin rounded-full border-2 border-white/15 border-t-signal"
			role="status"
			aria-label="Loading desktop"
		></span>
	</div>
{/if}
