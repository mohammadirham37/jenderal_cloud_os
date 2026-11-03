<script lang="ts">
	import { Plus } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { Alert, Button, PageHeader, Tabs } from '#lib/components/ui/index.ts';
	import GpuInventory from '#lib/components/gpu/GpuInventory.svelte';
	import GpuRequestForm from '#lib/components/gpu/GpuRequestForm.svelte';
	import GpuMyRequests from '#lib/components/gpu/GpuMyRequests.svelte';
	import GpuApprovals from '#lib/components/gpu/GpuApprovals.svelte';
	import GpuAllocations from '#lib/components/gpu/GpuAllocations.svelte';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { GPURequest } from '#lib/api/types.ts';
	import type { GPUInventory } from '#lib/api/types-gpu.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';

	const canView = $derived(auth.can('gpu.view', 'gpu.manage', 'gpu.approve'));
	const canRequest = $derived(auth.can('gpu.request'));
	const canApprove = $derived(auth.can('gpu.approve'));
	const canSeeRequests = $derived(auth.can('gpu.view', 'gpu.request', 'gpu.approve'));

	const inventory = resource<GPUInventory | null>(
		async () => (canView ? api.get<GPUInventory>('/gpu/devices') : null),
		{ poll: 30000 }
	);
	const myRequests = resource<GPURequest[]>(async () =>
		canSeeRequests ? api.get<GPURequest[]>('/gpu/requests', { mine: 'true' }) : []
	);
	const pending = resource<GPURequest[]>(
		async () => (canApprove ? api.get<GPURequest[]>('/gpu/requests', { status: 'pending' }) : []),
		{ poll: 30000 }
	);

	const tabs = $derived(
		[
			canView && { id: 'inventory', label: 'Inventory', count: inventory.data?.devices.length },
			canRequest && { id: 'request', label: 'Request GPU' },
			canSeeRequests && {
				id: 'requests',
				label: 'My requests',
				count: myRequests.data?.filter((r) => r.status === 'pending').length || undefined
			},
			canApprove && { id: 'approvals', label: 'Approvals', count: pending.data?.length },
			canView && { id: 'allocations', label: 'Allocations' }
		].filter((t): t is { id: string; label: string; count?: number } => !!t)
	);

	// Tab ids depend only on permissions so polling does not reset the tab.
	const tabIds = $derived(tabs.map((t) => t.id).join(','));

	function fromUrl(ids: string[]): string {
		const t = page.url.searchParams.get('tab');
		return t && ids.includes(t) ? t : (ids[0] ?? '');
	}

	// Follows ?tab= in the URL; overwritten locally when a tab is selected.
	let active = $derived(fromUrl(tabIds.split(',')));

	function select(id: string) {
		active = id;
		goto(`?tab=${id}`, { replace: true, reset: false });
	}

	const gpuEnabled = $derived(inventory.data?.gpu_enabled ?? true);
</script>

<PageHeader
	title="GPU Center"
	description="GPU inventory, access requests, approvals and active allocations."
>
	{#snippet actions()}
		{#if canRequest && active !== 'request'}
			<Button variant="primary" onclick={() => select('request')}
				><Plus class="size-4" /> Request GPU</Button
			>
		{/if}
	{/snippet}
</PageHeader>

{#if tabs.length === 0}
	<Alert tone="info" title="No access">Your role does not include access to the GPU Center.</Alert>
{:else}
	<Tabs {tabs} bind:active onchange={select} />

	<div class="mt-6" role="tabpanel" aria-labelledby="tab-{active}">
		{#if active === 'inventory'}
			<GpuInventory {inventory} />
		{:else if active === 'request'}
			<GpuRequestForm
				devices={inventory.data?.devices ?? []}
				{gpuEnabled}
				onsubmitted={() => {
					myRequests.reload();
					pending.reload();
					select('requests');
				}}
			/>
		{:else if active === 'requests'}
			<GpuMyRequests
				requests={myRequests}
				onnew={canRequest ? () => select('request') : undefined}
			/>
		{:else if active === 'approvals'}
			<GpuApprovals
				requests={pending}
				devices={inventory.data?.devices ?? []}
				onchange={() => inventory.reload()}
			/>
		{:else if active === 'allocations'}
			<GpuAllocations onchange={() => inventory.reload()} />
		{/if}
	</div>
{/if}
