<script lang="ts">
	// Overview tab: limits, storage, live metrics, history and timestamps.
	import { untrack } from 'svelte';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { WorkspaceDetail } from '#lib/api/types.ts';
	import type { WorkspaceMetrics } from '#lib/api/types-workspaces.ts';
	import { Card, Meter, Skeleton } from '#lib/components/ui/index.ts';
	import { bytes, cores, dateTime, percent, relative } from '#lib/format.ts';
	import HistoryChart from './HistoryChart.svelte';
	import { idleLabel } from './workspace.ts';

	let { ws }: { ws: WorkspaceDetail } = $props();

	const live = $derived(ws.grants.Operate && ws.status === 'running');
	const metrics = resource(
		() =>
			live
				? api.get<WorkspaceMetrics>(`/monitoring/workspaces/${ws.id}`)
				: Promise.resolve(undefined),
		{ immediate: false }
	);
	$effect(() => {
		if (!live) return;
		untrack(() => metrics.reload());
		const t = setInterval(() => {
			if (document.visibilityState === 'visible') metrics.reload();
		}, 10000);
		return () => clearInterval(t);
	});

	const cpuOf = (id: string) => ws.containers.find((x) => x.id === id)?.cpu_millis ?? ws.cpu_millis;

	const storageDisplay = $derived(
		ws.storage_used_bytes === null
			? undefined
			: `${bytes(ws.storage_used_bytes)} of ${bytes(ws.storage_bytes)}`
	);
</script>

<div class="grid gap-6 lg:grid-cols-3">
	<Card title="Resources" class="lg:col-span-1">
		<dl class="space-y-2 text-sm">
			<div class="flex justify-between gap-3">
				<dt class="text-fg-muted">CPU limit</dt>
				<dd class="tabular-nums">{cores(ws.cpu_millis)}</dd>
			</div>
			<div class="flex justify-between gap-3">
				<dt class="text-fg-muted">Memory limit</dt>
				<dd class="tabular-nums">{bytes(ws.memory_bytes)}</dd>
			</div>
			<div class="flex justify-between gap-3">
				<dt class="text-fg-muted">Storage</dt>
				<dd class="tabular-nums">{bytes(ws.storage_bytes)}</dd>
			</div>
			<div class="flex justify-between gap-3">
				<dt class="text-fg-muted">Process limit</dt>
				<dd class="tabular-nums">{ws.pids_limit}</dd>
			</div>
			<div class="flex justify-between gap-3">
				<dt class="text-fg-muted">Idle timeout</dt>
				<dd>{idleLabel(ws.idle_timeout_min)}</dd>
			</div>
			<div class="flex justify-between gap-3">
				<dt class="text-fg-muted">Max. run time</dt>
				<dd>{ws.max_duration_min ? idleLabel(ws.max_duration_min) : 'Unlimited'}</dd>
			</div>
		</dl>
		<div class="mt-4 border-t border-border pt-4">
			{#if ws.storage_used_bytes === null}
				<div class="flex justify-between text-xs">
					<span class="text-fg-muted">Storage used</span>
					<span class="text-fg-muted">Not measured yet</span>
				</div>
				<p class="mt-1 text-xs text-fg-subtle">Usage is measured periodically by the server.</p>
			{:else}
				<Meter
					label="Storage used"
					value={ws.storage_used_bytes}
					max={ws.storage_bytes}
					display={storageDisplay}
				/>
			{/if}
		</div>
	</Card>

	<Card title="Live usage" class="lg:col-span-1">
		{#if !ws.grants.Operate}
			<p class="text-sm text-fg-muted">You need operator access to view live metrics.</p>
		{:else if ws.status !== 'running'}
			<p class="text-sm text-fg-muted">
				Live metrics are available while the workspace is running.
			</p>
		{:else if !metrics.data && !metrics.error}
			<Skeleton lines={3} />
		{:else if metrics.error || metrics.data?.error}
			<p class="text-sm text-fg-muted">
				<span class="font-medium text-fg">Unavailable.</span>
				{metrics.data?.error ?? 'Live metrics could not be loaded from the host agent.'}
			</p>
		{:else if metrics.data && metrics.data.containers.length === 0}
			<p class="text-sm text-fg-muted">No running containers report metrics yet.</p>
		{:else if metrics.data}
			<ul class="space-y-4">
				{#each metrics.data.containers as c (c.container_id)}
					{@const cpuLimit = cpuOf(c.container_id)}
					<li>
						<p class="mb-2 text-sm font-medium">{c.name}</p>
						<div class="space-y-2">
							<Meter
								label="CPU ({percent(c.cpu_percent)} of one core)"
								value={c.cpu_percent}
								max={(cpuLimit / 1000) * 100}
								display="{(c.cpu_percent / 100).toFixed(2)} of {cores(cpuLimit)}"
							/>
							<Meter
								label="Memory"
								value={c.memory_bytes}
								max={c.memory_limit}
								display="{bytes(c.memory_bytes)} of {bytes(c.memory_limit)}"
							/>
							<p class="text-xs text-fg-muted">{c.pids} processes</p>
						</div>
					</li>
				{/each}
			</ul>
		{/if}
	</Card>

	<Card title="Timeline" class="lg:col-span-1">
		<dl class="space-y-2 text-sm">
			<div class="flex justify-between gap-3">
				<dt class="text-fg-muted">Created</dt>
				<dd title={dateTime(ws.created_at)}>{dateTime(ws.created_at)}</dd>
			</div>
			<div class="flex justify-between gap-3">
				<dt class="text-fg-muted">Started</dt>
				<dd title={dateTime(ws.started_at)}>
					{ws.started_at ? relative(ws.started_at) : '—'}
				</dd>
			</div>
			<div class="flex justify-between gap-3">
				<dt class="text-fg-muted">Last activity</dt>
				<dd title={dateTime(ws.last_activity_at)}>
					{ws.last_activity_at ? relative(ws.last_activity_at) : 'No activity recorded'}
				</dd>
			</div>
			<div class="flex justify-between gap-3">
				<dt class="text-fg-muted">Updated</dt>
				<dd title={dateTime(ws.updated_at)}>{relative(ws.updated_at)}</dd>
			</div>
		</dl>
	</Card>
</div>

{#if ws.grants.Operate}
	<div class="mt-6">
		<HistoryChart scope="workspace" ref={ws.id} memoryLimit={ws.memory_bytes} />
	</div>
{/if}
