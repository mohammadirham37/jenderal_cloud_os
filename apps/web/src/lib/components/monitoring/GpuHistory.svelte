<script lang="ts">
	// History of one GPU (GET /monitoring/history?scope=gpu&ref={uuid}).
	import { ErrorState, LineChart, Skeleton } from '#lib/components/ui/index.ts';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { HistoryRange, MonitoringHistory } from '#lib/api/types-admin.ts';
	import { bytes } from '#lib/format.ts';
	import RangePicker from './RangePicker.svelte';

	let { uuid, name }: { uuid: string; name: string } = $props();
	let range = $state<HistoryRange>('1h');
	const hist = resource(
		() => api.get<MonitoringHistory>('/monitoring/history', { scope: 'gpu', ref: uuid, range }),
		{ poll: 60000 }
	);
	const pts = $derived(hist.data?.points ?? []);
	const times = $derived(pts.map((p) => p.t));
	const memMax = $derived.by(() => {
		const totals = pts.map((p) => p.memory_total_bytes).filter((v): v is number => v !== null);
		return totals.length ? Math.max(...totals) : undefined;
	});
</script>

<div class="space-y-4">
	<div class="flex justify-end"><RangePicker bind:value={range} label="GPU history range" /></div>
	{#if hist.error}
		<ErrorState error={hist.error} retry={hist.reload} />
	{:else if !hist.data}
		<Skeleton lines={5} />
	{:else}
		<div class="grid gap-6 lg:grid-cols-3">
			<div>
				<h4 class="mb-2 text-xs font-medium text-fg-muted">Utilization</h4>
				<LineChart
					title="{name} utilization (%)"
					{times}
					height={140}
					yMax={100}
					format={(v) => `${v.toFixed(0)}%`}
					series={[
						{ label: 'Util', values: pts.map((p) => p.gpu_util_pct), color: 'var(--color-accent)' }
					]}
				/>
			</div>
			<div>
				<h4 class="mb-2 text-xs font-medium text-fg-muted">Memory used</h4>
				<LineChart
					title="{name} memory used"
					{times}
					height={140}
					yMax={memMax}
					format={(v) => bytes(v, 0)}
					series={[
						{
							label: 'Memory',
							values: pts.map((p) => p.gpu_memory_bytes),
							color: 'var(--color-info)'
						}
					]}
				/>
			</div>
			<div>
				<h4 class="mb-2 text-xs font-medium text-fg-muted">Temperature</h4>
				<LineChart
					title="{name} temperature (°C)"
					{times}
					height={140}
					format={(v) => `${v.toFixed(0)}°C`}
					series={[
						{
							label: 'Temp',
							values: pts.map((p) => p.gpu_temperature_c),
							color: 'var(--color-warning)'
						}
					]}
				/>
			</div>
		</div>
	{/if}
</div>
