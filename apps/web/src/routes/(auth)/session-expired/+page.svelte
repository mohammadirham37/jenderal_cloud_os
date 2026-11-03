<script lang="ts">
	import { page } from '$app/state';
	import { Button } from '#lib/components/ui/index.ts';
	import { Clock } from '@lucide/svelte';

	const next = $derived.by(() => {
		const n = page.url.searchParams.get('next') ?? '/';
		return n.startsWith('/') && !n.startsWith('//') ? n : '/';
	});
</script>

<svelte:head><title>Session expired · Jenderal Cloud OS</title></svelte:head>

<div class="text-center">
	<div
		class="mx-auto mb-4 flex size-12 items-center justify-center rounded-full bg-warning-soft text-warning"
	>
		<Clock class="size-6" />
	</div>
	<h1 class="text-xl font-semibold">Your session has ended</h1>
	<p class="mt-2 text-sm text-fg-muted">
		For security, sessions end after a period of inactivity or when revoked by an administrator.
		Sign in again to continue.
	</p>
	<Button href="/login?next={encodeURIComponent(next)}" variant="primary" class="mt-6 w-full"
		>Sign in again</Button
	>
</div>
