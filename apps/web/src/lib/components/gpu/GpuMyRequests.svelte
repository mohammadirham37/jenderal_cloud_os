<script lang="ts">
	import { Inbox, Plus } from '@lucide/svelte';
	import { Button, Card, DataTable, EmptyState, StatusBadge } from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';
	import type { Resource } from '#lib/api/resource.svelte.ts';
	import type { GPURequest } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { dateTime, relative, titleCase } from '#lib/format.ts';

	let { requests, onnew }: { requests: Resource<GPURequest[]>; onnew?: () => void } = $props();
	let busy = $state<string | null>(null);

	async function cancel(r: GPURequest) {
		const ok = await confirmDialog.confirm({
			title: 'Cancel GPU request?',
			message: `The pending request for ${r.workspace_name} is withdrawn. You can submit a new one later.`,
			confirmLabel: 'Cancel request',
			cancelLabel: 'Keep request',
			danger: true
		});
		if (!ok) return;
		busy = r.id;
		try {
			await api.post(`/gpu/requests/${r.id}/cancel`);
			toast.success('Request cancelled');
			await requests.reload();
		} catch (err) {
			toast.error('Could not cancel the request', errorMessage(err));
		} finally {
			busy = null;
		}
	}
</script>

<Card padded={false}>
	<DataTable
		columns={[
			{ key: 'workspace', label: 'Workspace' },
			{ key: 'gpu', label: 'GPU' },
			{ key: 'mode', label: 'Mode' },
			{ key: 'duration', label: 'Duration', class: 'text-right' },
			{ key: 'status', label: 'Status' },
			{ key: 'review', label: 'Review' },
			{ key: 'created', label: 'Requested' },
			{ key: 'actions', label: 'Actions', class: 'text-right' }
		]}
		rows={requests.data ?? []}
		loading={requests.loading}
		error={requests.error}
		retry={requests.reload}
	>
		{#snippet row(r: GPURequest)}
			<tr>
				<td class="px-4 py-3">
					<a href="/workspaces/{r.workspace_id}" class="font-medium hover:underline"
						>{r.workspace_name}</a
					>
					<p class="max-w-64 truncate text-xs text-fg-muted" title={r.reason}>{r.reason}</p>
				</td>
				<td class="px-4 py-3 text-fg-muted">{r.gpu_name ?? 'Any'}</td>
				<td class="px-4 py-3">{titleCase(r.mode)}</td>
				<td class="px-4 py-3 text-right tabular-nums">{r.duration_hours} h</td>
				<td class="px-4 py-3"><StatusBadge status={r.status} /></td>
				<td class="px-4 py-3 text-xs text-fg-muted">
					{#if r.reviewed_by_username}
						<p>{r.reviewed_by_username} · {relative(r.reviewed_at)}</p>
						{#if r.review_note}<p class="max-w-64 text-fg">“{r.review_note}”</p>{/if}
					{:else}—{/if}
				</td>
				<td class="px-4 py-3 whitespace-nowrap text-fg-muted" title={dateTime(r.created_at)}
					>{relative(r.created_at)}</td
				>
				<td class="px-4 py-3 text-right">
					{#if r.status === 'pending'}
						<Button size="sm" onclick={() => cancel(r)} loading={busy === r.id}>Cancel</Button>
					{/if}
				</td>
			</tr>
		{/snippet}
		{#snippet empty()}
			<EmptyState
				icon={Inbox}
				title="No GPU requests"
				description="Requests you submit and their review status appear here."
			>
				{#snippet action()}
					{#if auth.can('gpu.request') && onnew}
						<Button variant="primary" onclick={onnew}><Plus class="size-4" /> Request a GPU</Button>
					{/if}
				{/snippet}
			</EmptyState>
		{/snippet}
	</DataTable>
</Card>
