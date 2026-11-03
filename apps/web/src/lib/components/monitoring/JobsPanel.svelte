<script lang="ts">
	// Background jobs by status (GET /monitoring/jobs) with retry of dead jobs.
	import { RotateCcw } from '@lucide/svelte';
	import { Button, Card, DataTable, StatusBadge } from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { BackgroundJob } from '#lib/api/types-admin.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { dateTime, relative } from '#lib/format.ts';

	let status = $state<'dead' | 'queued' | 'running'>('dead');
	const jobs = resource(() => api.get<BackgroundJob[]>('/monitoring/jobs', { status }), {
		poll: 15000
	});
	const canRetry = $derived(auth.can('settings.manage', 'workspaces.manage_all'));
	let busy = $state<string | null>(null);

	async function retry(j: BackgroundJob) {
		const ok = await confirmDialog.confirm({
			title: 'Retry this job?',
			message: `The ${j.kind} job will be queued again with its attempt counter reset. Make sure the cause of the failure has been fixed first.`,
			confirmLabel: 'Retry job'
		});
		if (!ok) return;
		busy = j.id;
		try {
			await api.post(`/monitoring/jobs/${j.id}/retry`);
			toast.success('Job queued for retry', j.kind);
			await jobs.reload();
		} catch (e) {
			toast.error('Could not retry the job', errorMessage(e));
		} finally {
			busy = null;
		}
	}

	function resourceLink(j: BackgroundJob): string | null {
		if (!j.resource_id) return null;
		switch (j.resource_type) {
			case 'workspace':
				return `/workspaces/${j.resource_id}`;
			case 'container':
				return `/containers/${j.resource_id}`;
			case 'database':
				return `/databases/${j.resource_id}`;
			case 'backup':
				return '/admin/backups';
			default:
				return null;
		}
	}
	const columns = $derived([
		{ key: 'kind', label: 'Job' },
		{ key: 'status', label: 'Status' },
		{ key: 'attempts', label: 'Attempts' },
		{ key: 'resource', label: 'Resource' },
		{ key: 'updated', label: 'Updated' },
		...(canRetry && status === 'dead'
			? [{ key: 'actions', label: 'Actions', class: 'text-right' }]
			: [])
	]);
	const descriptions = {
		dead: 'Jobs that failed permanently after exhausting their retries (latest 100).',
		queued: 'Jobs waiting for the worker (latest 100).',
		running: 'Jobs currently being processed by the worker.'
	};
</script>

<Card padded={false} description={descriptions[status]} title="Background jobs">
	{#snippet actions()}
		<label class="flex items-center gap-2 text-sm text-fg-muted" for="job-status">
			Status
			<select id="job-status" class="input h-8 w-auto py-0" bind:value={status}>
				<option value="dead">Dead</option>
				<option value="queued">Queued</option>
				<option value="running">Running</option>
			</select>
		</label>
	{/snippet}
	<DataTable
		{columns}
		rows={jobs.data ?? []}
		loading={jobs.loading}
		error={jobs.data ? null : jobs.error}
		retry={jobs.reload}
		emptyTitle={status === 'dead' ? 'No failed jobs' : `No ${status} jobs`}
		emptyDescription={status === 'dead'
			? 'Every background job completed or is still retrying.'
			: 'The queue is idle.'}
	>
		{#snippet row(j)}
			{@const link = resourceLink(j)}
			<tr class="align-top">
				<td class="px-4 py-3">
					<p class="font-mono text-xs font-medium">{j.kind}</p>
					{#if j.last_error}<p class="mt-1 max-w-xl text-xs break-words text-danger">
							{j.last_error}
						</p>{/if}
				</td>
				<td class="px-4 py-3"><StatusBadge status={j.status} /></td>
				<td class="px-4 py-3 tabular-nums">{j.attempts} / {j.max_attempts}</td>
				<td class="px-4 py-3 text-xs">
					{#if j.resource_type}
						{j.resource_type}
						{#if link}<a class="link font-mono" href={link}>{j.resource_id?.slice(0, 8)}</a
							>{:else if j.resource_id}<span class="font-mono text-fg-muted"
								>{j.resource_id.slice(0, 8)}</span
							>{/if}
					{:else}<span class="text-fg-muted">—</span>{/if}
				</td>
				<td class="px-4 py-3 text-xs whitespace-nowrap" title={dateTime(j.updated_at)}
					>{relative(j.updated_at)}</td
				>
				{#if canRetry && status === 'dead'}
					<td class="px-4 py-3 text-right">
						<Button size="sm" loading={busy === j.id} onclick={() => retry(j)}
							><RotateCcw class="size-3.5" /> Retry</Button
						>
					</td>
				{/if}
			</tr>
		{/snippet}
	</DataTable>
</Card>
