<script lang="ts">
	import { afterNavigate, goto } from '$app/navigation';
	import { page } from '$app/state';
	import { onMount } from 'svelte';
	import Sidebar from '#lib/components/layout/Sidebar.svelte';
	import Topbar from '#lib/components/layout/Topbar.svelte';
	import { Spinner } from '#lib/components/ui/index.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { notificationCounter } from '#lib/stores/notifications.svelte.ts';
	import { interceptCrossAppLinks, isEmbedded, postToShell } from '#lib/desktop/embed.ts';
	import type { LayoutProps } from './$types';

	let { children }: LayoutProps = $props();
	let collapsed = $state(false);
	let mobileOpen = $state(false);
	let ready = $state(false);
	// Inside a desktop window the page renders without the classic chrome.
	const embedded = isEmbedded();

	afterNavigate(() => {
		if (!embedded) return;
		// Let SvelteKit update <title> first, then report it to the shell.
		requestAnimationFrame(() =>
			postToShell({
				type: 'itk:nav',
				title: document.title.replace(/\s*·\s*ITK Cloud$/, ''),
				path: location.pathname + location.search
			})
		);
	});

	onMount(() => {
		const stopIntercept = embedded ? interceptCrossAppLinks() : () => {};
		collapsed = localStorage.getItem('itk-sidebar-collapsed') === 'true';
		(async () => {
			const me = auth.me ?? (await auth.load());
			if (!me) {
				await goto(`/login?next=${encodeURIComponent(page.url.pathname + page.url.search)}`, {
					replace: true
				});
				return;
			}
			const gate = auth.gate();
			if (
				gate &&
				!(gate.startsWith('/account/password') && page.url.pathname === '/account/password')
			) {
				await goto(gate, { replace: true });
				return;
			}
			ready = true;
			if (!embedded) notificationCounter.start();
		})();
		return () => {
			stopIntercept();
			notificationCounter.stop();
		};
	});
</script>

{#if !ready}
	<div class="flex h-screen items-center justify-center text-fg-muted">
		<Spinner size={24} /><span class="sr-only">Loading</span>
	</div>
{:else if embedded}
	<main id="main" class="min-h-screen w-full px-4 py-5 sm:px-6">
		{@render children()}
	</main>
{:else}
	<a
		href="#main"
		class="sr-only focus:not-sr-only focus:absolute focus:top-2 focus:left-2 focus:z-50 focus:rounded focus:bg-panel focus:px-3 focus:py-2"
		>Skip to content</a
	>
	<div class="flex min-h-screen">
		<Sidebar bind:collapsed bind:mobileOpen />
		<div class="flex min-w-0 flex-1 flex-col">
			<Topbar onmenu={() => (mobileOpen = true)} />
			<main id="main" class="mx-auto w-full max-w-[1400px] flex-1 px-4 py-6 lg:px-8">
				{@render children()}
			</main>
		</div>
	</div>
{/if}
