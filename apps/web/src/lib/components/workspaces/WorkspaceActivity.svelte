<script lang="ts">
	// Activity tab: recent lifecycle jobs (GET /workspaces/{id}/jobs).
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { WorkspaceJob } from '#lib/api/types-workspaces.ts';
	import { Button, Card, DataTable, StatusBadge } from '#lib/components/ui/index.ts';
	import { dateTime, relative, titleCase } from '#lib/format.ts';

	let { id }: { id: string } = $props();

	const jobs = resource(() => api.get<WorkspaceJob[]>(`/workspaces/${id}/jobs`), { poll: 10000 });

	const columns = [
		{ key: 'kind', label: 'Operation' },
		{ key: 'status', label: 'Status' },
		{ key: 'attempts', label: 'Attempts', class: 'text-right' },
		{ key: 'error', label: 'Last error' },
		{ key: 'created', label: 'Requested' },
		{ key: 'finished', label: 'Finished' }
	];
</script>

<Card
	title="Activity"
	description="The 20 most recent operations on this workspace and its containers."
	padded={false}
>
	{#snippet actions()}
		<Button size="sm" variant="ghost" loading={jobs.loading} onclick={jobs.reload}>Refresh</Button>
	{/snippet}
	<DataTable
		{columns}
		rows={jobs.data ?? []}
		loading={!jobs.data && jobs.loading}
		error={jobs.error}
		retry={jobs.reload}
		emptyTitle="No operations yet"
		emptyDescription="Start, stop and provisioning operations will be listed here."
	>
		{#snippet row(j: WorkspaceJob)}
			<tr>
				<td class="px-4 py-2.5 whitespace-nowrap">{titleCase(j.kind.replace('.', ' '))}</td>
				<td class="px-4 py-2.5"><StatusBadge status={j.status} /></td>
				<td class="px-4 py-2.5 text-right tabular-nums">{j.attempts}</td>
				<td class="max-w-sm px-4 py-2.5 text-xs">
					{#if j.last_error}
						<span class="line-clamp-2 text-danger" title={j.last_error}>{j.last_error}</span>
					{:else}<span class="text-fg-subtle">—</span>{/if}
				</td>
				<td class="px-4 py-2.5 whitespace-nowrap text-fg-muted" title={dateTime(j.created_at)}>
					{relative(j.created_at)}
				</td>
				<td class="px-4 py-2.5 whitespace-nowrap text-fg-muted" title={dateTime(j.finished_at)}>
					{j.finished_at ? relative(j.finished_at) : '—'}
				</td>
			</tr>
		{/snippet}
	</DataTable>
</Card>
