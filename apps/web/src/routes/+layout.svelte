<script lang="ts">
	import './layout.css';
	import favicon from '#lib/assets/favicon.svg';
	import { onMount } from 'svelte';
	import { Toaster, ConfirmDialog } from '#lib/components/ui/index.ts';
	import { theme } from '#lib/stores/theme.svelte.ts';
	import { appConfig } from '#lib/stores/config.svelte.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();

	onMount(() => {
		theme.init();
		appConfig.load();
	});
</script>

<svelte:head>
	<link rel="icon" href={favicon} />
</svelte:head>

{@render children()}
<Toaster />
<ConfirmDialog />
