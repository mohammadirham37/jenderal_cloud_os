<script lang="ts">
	import { Cpu } from '@lucide/svelte';
	import {
		Button,
		Card,
		DataTable,
		EmptyState,
		StatusBadge,
		Switch
	} from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { GPUAllocation } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { dateTime, relative, titleCase } from '#lib/format.ts';

	let { onchange }: { onchange?: () => void } = $props();

	let activeOnly = $state(true);
	const allocations = resource(
		() =>
			api.get<GPUAllocation[]>('/gpu/allocations', { status: activeOnly ? 'active' : undefined }),
		{ poll: 30000 }
	);
	const meId = $derived(auth.me?.id);
	let busy = $state<string | null>(null);

	function canRelease(a: GPUAllocation) {
		return a.status === 'active' && (a.user_id === meId || auth.can('gpu.approve', 'gpu.manage'));
	}

	async function release(a: GPUAllocation) {
		const { confirmed, reason } = await confirmDialog.ask({
			title: `Release GPU from ${a.workspace_name}?`,
			message:
				'The allocation ends now. If the workspace is running it restarts to detach the GPU, which interrupts running jobs.',
			reasonLabel: 'Reason (optional)',
			confirmLabel: 'Release GPU',
			danger: true
		});
		if (!confirmed) return;
		busy = a.id;
		try {
			await api.post(`/gpu/allocations/${a.id}/release`, reason ? { reason } : undefined);
			toast.success('GPU released', `#${a.gpu_index} ${a.gpu_name}`);
			await allocations.reload();
			onchange?.();
		} catch (err) {
			toast.error('Could not release the GPU', errorMessage(err));
		} finally {
			busy = null;
		}
	}
</script>

<Card padded={false}>
	<div class="flex items-center gap-2 border-b border-border px-4 py-3">
		<Switch id="alloc-active" label="Show active allocations only" bind:checked={activeOnly} />
		<label for="alloc-active" class="text-sm text-fg-muted">Active only</label>
	</div>
	<DataTable
		columns={[
			{ key: 'gpu', label: 'GPU' },
			{ key: 'workspace', label: 'Workspace' },
			{ key: 'user', label: 'User' },
			{ key: 'mode', label: 'Mode' },
			{ key: 'status', label: 'Status' },
			{ key: 'starts', label: 'Starts' },
			{ key: 'ends', label: 'Ends' },
			{ key: 'actions', label: 'Actions', class: 'text-right' }
		]}
		rows={allocations.data ?? []}
		loading={allocations.loading}
		error={allocations.error}
		retry={allocations.reload}
	>
		{#snippet row(a: GPUAllocation)}
			<tr>
				<td class="px-4 py-3"><span class="text-fg-muted">#{a.gpu_index}</span> {a.gpu_name}</td>
				<td class="px-4 py-3">
					<a href="/workspaces/{a.workspace_id}" class="hover:underline">{a.workspace_name}</a>
				</td>
				<td class="px-4 py-3 text-fg-muted">{a.username}</td>
				<td class="px-4 py-3">{titleCase(a.mode)}</td>
				<td class="px-4 py-3">
					<StatusBadge status={a.status} />
					{#if a.release_reason}
						<p class="mt-1 max-w-48 truncate text-xs text-fg-muted" title={a.release_reason}>
							{a.release_reason}
						</p>
					{/if}
				</td>
				<td class="px-4 py-3 whitespace-nowrap text-fg-muted" title={dateTime(a.starts_at)}
					>{relative(a.starts_at)}</td
				>
				<td class="px-4 py-3 whitespace-nowrap" title={dateTime(a.released_at ?? a.ends_at)}>
					{a.status === 'active' ? relative(a.ends_at) : relative(a.released_at ?? a.ends_at)}
				</td>
				<td class="px-4 py-3 text-right">
					{#if canRelease(a)}
						<Button
							size="sm"
							onclick={() => release(a)}
							loading={busy === a.id}
							disabled={busy !== null}>Release</Button
						>
					{/if}
				</td>
			</tr>
		{/snippet}
		{#snippet empty()}
			<EmptyState
				icon={Cpu}
				title={activeOnly ? 'No active GPU allocations' : 'No GPU allocations'}
				description="Approved GPU requests create allocations that appear here until they end or are released."
			/>
		{/snippet}
	</DataTable>
</Card>
