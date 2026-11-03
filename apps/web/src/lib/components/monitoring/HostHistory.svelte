<script lang="ts">
	// Host metric history charts (GET /monitoring/history?scope=host).
	import { Card, ErrorState, LineChart, Skeleton } from '#lib/components/ui/index.ts';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { HistoryRange, MonitoringHistory } from '#lib/api/types-admin.ts';
	import { bytes, rate } from '#lib/format.ts';
	import RangePicker from './RangePicker.svelte';

	let range = $state<HistoryRange>('1h');
	// `range` is read synchronously, so changing it reloads the resource.
	const hist = resource(
		() => api.get<MonitoringHistory>('/monitoring/history', { scope: 'host', range }),
		{ poll: 60000 }
	);
	const pts = $derived(hist.data?.points ?? []);
	const times = $derived(pts.map((p) => p.t));
	const memMax = $derived.by(() => {
		const totals = pts.map((p) => p.memory_total_bytes).filter((v): v is number => v !== null);
		return totals.length ? Math.max(...totals) : undefined;
	});
</script>

<div class="mb-4 flex flex-wrap items-center justify-between gap-3">
	<p class="text-sm text-fg-muted">
		Averages per {hist.data
			? hist.data.bucket_seconds >= 3600
				? `${hist.data.bucket_seconds / 3600} h`
				: `${hist.data.bucket_seconds / 60} min`
			: '…'} bucket. Gaps mean no samples were collected (for example while the agent was offline).
	</p>
	<RangePicker bind:value={range} />
</div>

{#if hist.error}
	<Card><ErrorState error={hist.error} retry={hist.reload} /></Card>
{:else if !hist.data}
	<div class="grid gap-6 lg:grid-cols-2">
		{#each [1, 2, 3, 4] as i (i)}<div class="card p-5"><Skeleton lines={6} /></div>{/each}
	</div>
{:else}
	<div class="grid gap-6 lg:grid-cols-2" aria-busy={hist.loading}>
		<Card title="CPU usage">
			<LineChart
				title="Host CPU usage (%)"
				{times}
				yMax={100}
				format={(v) => `${v.toFixed(0)}%`}
				series={[{ label: 'CPU', values: pts.map((p) => p.cpu_pct), color: 'var(--color-accent)' }]}
			/>
		</Card>
		<Card title="Memory used">
			<LineChart
				title="Host memory used"
				{times}
				yMax={memMax}
				format={(v) => bytes(v, 0)}
				series={[
					{ label: 'Used', values: pts.map((p) => p.memory_bytes), color: 'var(--color-info)' }
				]}
			/>
		</Card>
		<Card title="Network throughput">
			<LineChart
				title="Host network throughput"
				{times}
				format={(v) => rate(v)}
				series={[
					{
						label: 'Received',
						values: pts.map((p) => p.net_rx_bytes_per_sec),
						color: 'var(--color-success)'
					},
					{
						label: 'Transmitted',
						values: pts.map((p) => p.net_tx_bytes_per_sec),
						color: 'var(--color-warning)'
					}
				]}
			/>
		</Card>
		<Card title="Load average (1 min)">
			<LineChart
				title="Host load average"
				{times}
				format={(v) => v.toFixed(1)}
				series={[
					{ label: 'Load 1', values: pts.map((p) => p.load1), color: 'var(--color-accent)' }
				]}
			/>
		</Card>
		<Card title="Workspace disk used" class="lg:col-span-2">
			<LineChart
				title="Disk used on the workspace filesystem"
				{times}
				format={(v) => bytes(v, 0)}
				series={[
					{
						label: 'Disk used',
						values: pts.map((p) => p.disk_used_bytes),
						color: 'var(--color-danger)'
					}
				]}
			/>
		</Card>
	</div>
{/if}
