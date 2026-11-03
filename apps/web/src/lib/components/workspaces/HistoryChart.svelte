<script lang="ts">
	// CPU and memory history for a workspace or container
	// (GET /monitoring/history?scope=&ref=&range=).
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { HistoryResponse } from '#lib/api/types-workspaces.ts';
	import { Card, ErrorState, LineChart, Skeleton } from '#lib/components/ui/index.ts';
	import { bytes, percent } from '#lib/format.ts';

	let {
		scope,
		ref,
		memoryLimit
	}: { scope: 'workspace' | 'container'; ref: string; memoryLimit?: number } = $props();

	const ranges = [
		{ id: '1h', label: '1 hour' },
		{ id: '6h', label: '6 hours' },
		{ id: '24h', label: '24 hours' }
	];
	let range = $state('1h');

	// Re-fetches automatically when `range` or `ref` change (read inside fn).
	const history = resource(
		() => api.get<HistoryResponse>('/monitoring/history', { scope, ref, range }),
		{ poll: 60000 }
	);

	const points = $derived(history.data?.points ?? []);
	const times = $derived(points.map((p) => p.t));
	const memLimit = $derived(
		memoryLimit ?? points.findLast((p) => p.memory_total_bytes !== null)?.memory_total_bytes ?? null
	);
</script>

<Card title="Usage history" description="CPU 100% equals one fully used core.">
	{#snippet actions()}
		<div class="flex gap-1" role="group" aria-label="History range">
			{#each ranges as r (r.id)}
				<button
					type="button"
					class="rounded-md px-2 py-1 text-xs font-medium {range === r.id
						? 'bg-accent-soft text-accent'
						: 'text-fg-muted hover:bg-panel-2 hover:text-fg'}"
					aria-pressed={range === r.id}
					onclick={() => (range = r.id)}>{r.label}</button
				>
			{/each}
		</div>
	{/snippet}
	{#if history.error}
		<ErrorState error={history.error} retry={history.reload} />
	{:else if !history.data}
		<Skeleton lines={4} />
	{:else}
		<div class="grid gap-6 lg:grid-cols-2">
			<div>
				<h3 class="mb-2 text-xs font-medium tracking-wide text-fg-muted uppercase">CPU</h3>
				<LineChart
					title="CPU usage over the last {range}"
					{times}
					series={[
						{ label: 'CPU', values: points.map((p) => p.cpu_pct), color: 'var(--color-accent)' }
					]}
					format={(v) => percent(v)}
				/>
			</div>
			<div>
				<h3 class="mb-2 text-xs font-medium tracking-wide text-fg-muted uppercase">Memory</h3>
				<LineChart
					title="Memory usage over the last {range}"
					{times}
					series={[
						{
							label: 'Used',
							values: points.map((p) => p.memory_bytes),
							color: 'var(--color-info)'
						}
					]}
					yMax={memLimit ?? undefined}
					format={(v) => bytes(v, 0)}
				/>
			</div>
		</div>
	{/if}
</Card>
