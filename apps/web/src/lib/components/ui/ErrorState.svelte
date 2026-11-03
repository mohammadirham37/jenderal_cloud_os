<script lang="ts">
	import { CircleAlert } from '@lucide/svelte';
	import Button from './Button.svelte';
	import { ApiError } from '#lib/api/client.ts';

	let { error, retry }: { error: unknown; retry?: () => void } = $props();
	const message = $derived(error instanceof Error ? error.message : 'Something went wrong.');
	const requestId = $derived(error instanceof ApiError ? error.requestId : undefined);
	const status = $derived(error instanceof ApiError ? error.status : 0);
</script>

<div class="flex flex-col items-center justify-center px-6 py-12 text-center" role="alert">
	<div class="mb-3 rounded-full bg-danger-soft p-3 text-danger">
		<CircleAlert class="size-6" aria-hidden="true" />
	</div>
	<h3 class="text-sm font-semibold text-fg">
		{status === 404 ? 'Not found' : status === 403 ? 'Access denied' : 'Could not load data'}
	</h3>
	<p class="mt-1 max-w-md text-sm text-fg-muted">{message}</p>
	{#if requestId}<p class="mt-1 font-mono text-xs text-fg-subtle">Request ID: {requestId}</p>{/if}
	{#if retry && status !== 404 && status !== 403}
		<div class="mt-4"><Button onclick={retry}>Try again</Button></div>
	{/if}
</div>
