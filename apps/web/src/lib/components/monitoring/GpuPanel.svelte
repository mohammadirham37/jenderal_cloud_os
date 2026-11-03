<script lang="ts">
	// Live GPU inventory (GET /monitoring/gpu) with per-GPU history.
	import { ChartLine, Cpu, Thermometer, Zap } from '@lucide/svelte';
	import {
		Alert,
		Badge,
		Button,
		Card,
		EmptyState,
		ErrorState,
		Meter,
		Skeleton
	} from '#lib/components/ui/index.ts';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { GPUInventory } from '#lib/api/types-admin.ts';
	import { bytes, percent } from '#lib/format.ts';
	import GpuHistory from './GpuHistory.svelte';

	const inv = resource(() => api.get<GPUInventory>('/monitoring/gpu'), { poll: 15000 });
	let expanded = $state<Record<string, boolean>>({});
	const gpus = $derived(inv.data?.gpus ?? []);
</script>

{#if inv.error && !inv.data}
	<Card><ErrorState error={inv.error} retry={inv.reload} /></Card>
{:else if !inv.data}
	<div class="card p-5"><Skeleton lines={6} /></div>
{:else if !inv.data.available}
	<Card>
		<EmptyState
			icon={Cpu}
			title="GPU metrics unavailable"
			description={inv.data.error ||
				'No NVIDIA driver was detected on the host, or the host agent did not respond. GPU features stay disabled until nvidia-smi works on the host.'}
		>
			{#snippet action()}<Button onclick={inv.reload} loading={inv.loading}>Check again</Button
				>{/snippet}
		</EmptyState>
	</Card>
{:else if gpus.length === 0}
	<Card>
		<EmptyState
			icon={Cpu}
			title="No GPUs detected"
			description="The NVIDIA driver responded but reported no GPUs on this host."
		/>
	</Card>
{:else}
	{#if inv.data.error}<div class="mb-4">
			<Alert tone="warning" title="Partial GPU data">{inv.data.error}</Alert>
		</div>{/if}
	<div class="space-y-6">
		{#each gpus as g (g.uuid)}
			<Card title="GPU {g.index} · {g.name}" description={g.uuid}>
				{#snippet actions()}
					{#if g.mig_mode}<Badge tone="info">MIG {g.mig_mode}</Badge>{/if}
					<Button
						size="sm"
						variant="ghost"
						aria-expanded={!!expanded[g.uuid]}
						onclick={() => (expanded[g.uuid] = !expanded[g.uuid])}
						><ChartLine class="size-3.5" />
						{expanded[g.uuid] ? 'Hide history' : 'Show history'}</Button
					>
				{/snippet}
				<div class="grid gap-6 md:grid-cols-2 xl:grid-cols-4">
					<Meter
						label="Utilization"
						value={g.utilization_pct}
						max={100}
						display={percent(g.utilization_pct)}
					/>
					<Meter
						label="Memory"
						value={g.memory_used_bytes}
						max={g.memory_total_bytes ?? 0}
						display="{bytes(g.memory_used_bytes)} / {bytes(g.memory_total_bytes)}"
					/>
					<div class="flex items-center gap-2 text-sm">
						<Thermometer class="size-4 text-fg-subtle" aria-hidden="true" />
						<span class="text-fg-muted">Temperature</span>
						<span class="ml-auto font-medium tabular-nums"
							>{g.temperature_c === null ? 'Unavailable' : `${g.temperature_c}°C`}</span
						>
					</div>
					<div class="flex items-center gap-2 text-sm">
						<Zap class="size-4 text-fg-subtle" aria-hidden="true" />
						<span class="text-fg-muted">Power draw</span>
						<span class="ml-auto font-medium tabular-nums"
							>{g.power_draw_w === null ? 'Unavailable' : `${g.power_draw_w.toFixed(0)} W`}</span
						>
					</div>
				</div>
				<p class="mt-3 text-xs text-fg-muted">
					Driver {g.driver_version || '—'}{#if g.cuda_version}
						· CUDA {g.cuda_version}{/if}
				</p>

				<div class="mt-4">
					<h3 class="mb-2 text-xs font-semibold tracking-wide text-fg-muted uppercase">
						Processes
					</h3>
					{#if g.processes === null}
						<p class="text-sm text-fg-muted">Process details are visible to administrators only.</p>
					{:else if g.processes.length === 0}
						<p class="text-sm text-fg-muted">No compute processes are running on this GPU.</p>
					{:else}
						<div class="overflow-x-auto rounded-md border border-border">
							<table class="w-full text-left text-sm">
								<thead class="bg-panel-2/60 text-xs text-fg-muted uppercase">
									<tr>
										<th scope="col" class="px-3 py-2 font-medium">PID</th>
										<th scope="col" class="px-3 py-2 font-medium">Process</th>
										<th scope="col" class="px-3 py-2 font-medium">Memory</th>
										<th scope="col" class="px-3 py-2 font-medium">Container</th>
									</tr>
								</thead>
								<tbody class="divide-y divide-border">
									{#each g.processes as p (p.pid)}
										<tr>
											<td class="px-3 py-2 font-mono text-xs">{p.pid}</td>
											<td class="px-3 py-2">{p.name}</td>
											<td class="px-3 py-2 tabular-nums">{bytes(p.memory_bytes)}</td>
											<td class="px-3 py-2">
												{#if p.container_id}<a
														class="link font-mono text-xs"
														href="/containers/{p.container_id}">{p.container_id.slice(0, 8)}</a
													>{:else}<span class="text-fg-muted">Not attributable</span>{/if}
											</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</div>
					{/if}
				</div>

				{#if expanded[g.uuid]}
					<div class="mt-5 border-t border-border pt-5">
						<GpuHistory uuid={g.uuid} name="GPU {g.index}" />
					</div>
				{/if}
			</Card>
		{/each}
	</div>
{/if}
