<script lang="ts">
	// Live host metrics (GET /monitoring/host), polled every 10 s. When the
	// agent does not respond, the last stored sample is shown with its time.
	import { ArrowDownUp, Clock, Container, Cpu, Gauge, MemoryStick, Server } from '@lucide/svelte';
	import {
		Alert,
		Card,
		EmptyState,
		ErrorState,
		Meter,
		Skeleton,
		StatCard
	} from '#lib/components/ui/index.ts';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { MonitoringHost } from '#lib/api/types-admin.ts';
	import { bytes, dateTime, duration, percent, rate, relative } from '#lib/format.ts';
	import CoreBars from './CoreBars.svelte';

	const host = resource(() => api.get<MonitoringHost>('/monitoring/host'), { poll: 10000 });
	const m = $derived(host.data?.live ? host.data.metrics : undefined);
	const last = $derived(host.data && !host.data.live ? host.data.last_sample : undefined);
	const fixed = (v: number | null | undefined, d = 2) =>
		v === null || v === undefined ? '—' : v.toFixed(d);
</script>

{#if host.error && !host.data}
	<Card><ErrorState error={host.error} retry={host.reload} /></Card>
{:else if !host.data}
	<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
		{#each [1, 2, 3, 4, 5, 6] as i (i)}<div class="card p-4"><Skeleton lines={2} /></div>{/each}
	</div>
{:else if m}
	<p class="mb-3 text-xs text-fg-muted">
		Live · collected {dateTime(m.collected_at)}{#if host.data.node?.hostname}
			· host <span class="font-mono">{host.data.node.hostname}</span>{/if} · refreshes every 10 seconds
	</p>
	<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
		<StatCard
			label="CPU usage"
			value={percent(m.cpu_percent, 1)}
			sub={m.per_core?.length ? `${m.per_core.length} logical cores` : undefined}
			icon={Cpu}
		>
			{#if m.per_core?.length}<CoreBars cores={m.per_core} />{/if}
		</StatCard>
		<StatCard
			label="Load average"
			value={fixed(m.load1)}
			sub="1 min · 5 min {fixed(m.load5)} · 15 min {fixed(m.load15)}"
			icon={Gauge}
		/>
		<StatCard
			label="Memory"
			value={bytes(m.memory_used)}
			sub="of {bytes(m.memory_total)} · {bytes(m.memory_available)} available"
			icon={MemoryStick}
		>
			<Meter label="Memory used" value={m.memory_used} max={m.memory_total} />
		</StatCard>
		<StatCard
			label="Swap"
			value={m.swap_total > 0 ? bytes(m.swap_used) : 'Not configured'}
			sub={m.swap_total > 0 ? `of ${bytes(m.swap_total)}` : 'The host has no swap space.'}
			icon={MemoryStick}
		>
			{#if m.swap_total > 0}<Meter label="Swap used" value={m.swap_used} max={m.swap_total} />{/if}
		</StatCard>
		<StatCard
			label="Uptime"
			value={duration(m.uptime_seconds)}
			sub={host.data.node?.host_info?.boot_time
				? `booted ${dateTime(host.data.node.host_info.boot_time)}`
				: undefined}
			icon={Clock}
		/>
		<StatCard
			label="Network"
			value="↓ {rate(m.net_rx_bytes_per_sec)}"
			sub="↑ {rate(m.net_tx_bytes_per_sec)} transmitted"
			icon={ArrowDownUp}
		/>
	</div>

	<div class="mt-6 grid gap-6 lg:grid-cols-3">
		<Card title="Disks" class="lg:col-span-2" padded={false}>
			{#if !m.disks?.length}
				<EmptyState
					title="No filesystems reported"
					description="The agent did not report any disks."
				/>
			{:else}
				<div class="overflow-x-auto">
					<table class="w-full text-left text-sm">
						<thead
							class="border-b border-border bg-panel-2/60 text-xs tracking-wide text-fg-muted uppercase"
						>
							<tr>
								<th scope="col" class="px-4 py-2.5 font-medium">Mount point</th>
								<th scope="col" class="px-4 py-2.5 font-medium">Type</th>
								<th scope="col" class="px-4 py-2.5 font-medium">Free</th>
								<th scope="col" class="w-2/5 px-4 py-2.5 font-medium">Usage</th>
							</tr>
						</thead>
						<tbody class="divide-y divide-border">
							{#each m.disks as d (d.mountpoint)}
								<tr>
									<td class="px-4 py-2.5 font-mono text-xs">{d.mountpoint}</td>
									<td class="px-4 py-2.5 text-fg-muted">{d.fstype}</td>
									<td class="px-4 py-2.5 tabular-nums">{bytes(d.free)}</td>
									<td class="px-4 py-2.5">
										<Meter
											label="{bytes(d.used)} of {bytes(d.total)}"
											value={d.used}
											max={d.total}
										/>
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</Card>

		<Card title="Container runtime">
			{#snippet actions()}<Container class="size-4 text-fg-subtle" aria-hidden="true" />{/snippet}
			{#if !m.docker.available}
				<Alert tone="danger" title="Docker is not available">
					{m.docker.error || 'The agent could not reach the Docker daemon.'}
				</Alert>
			{:else}
				<dl class="space-y-2 text-sm">
					<div class="flex justify-between">
						<dt class="text-fg-muted">Version</dt>
						<dd class="font-mono">{m.docker.version || '—'}</dd>
					</div>
					<div class="flex justify-between">
						<dt class="text-fg-muted">Running containers</dt>
						<dd class="tabular-nums">{m.docker.containers_running}</dd>
					</div>
					<div class="flex justify-between">
						<dt class="text-fg-muted">Stopped containers</dt>
						<dd class="tabular-nums">{m.docker.containers_stopped}</dd>
					</div>
					<div class="flex justify-between">
						<dt class="text-fg-muted">Paused containers</dt>
						<dd class="tabular-nums">{m.docker.containers_paused}</dd>
					</div>
					<div class="flex justify-between">
						<dt class="text-fg-muted">Images</dt>
						<dd class="tabular-nums">{m.docker.images}</dd>
					</div>
				</dl>
			{/if}
		</Card>
	</div>
{:else}
	<Alert tone="warning" title="The host agent is not responding">
		{host.data.error ?? 'Live metrics are unavailable.'} Workspace operations may fail until the agent
		is back. Check the agent service on the host (<code class="font-mono"
			>systemctl status itk-cloud-agent</code
		>) and the Host &amp; Agent page.
	</Alert>
	{#if last}
		<p class="mt-4 mb-3 text-xs text-fg-muted">
			Last stored sample from {dateTime(last.collected_at)} ({relative(last.collected_at)}). These
			values are not live.
		</p>
		<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
			<StatCard label="CPU usage" value={percent(last.cpu_percent, 1)} sub="Stale" icon={Cpu} />
			<StatCard label="Load (1 min)" value={fixed(last.load1)} sub="Stale" icon={Gauge} />
			<StatCard
				label="Memory"
				value={bytes(last.memory_used)}
				sub="of {bytes(last.memory_total)} · stale"
				icon={MemoryStick}
			>
				<Meter label="Memory used" value={last.memory_used} max={last.memory_total ?? 0} />
			</StatCard>
			<StatCard
				label="Workspace disk"
				value={bytes(last.disk_used)}
				sub="of {bytes(last.disk_total)} · stale"
				icon={Server}
			>
				<Meter label="Disk used" value={last.disk_used} max={last.disk_total ?? 0} />
			</StatCard>
			<StatCard
				label="Network"
				value="↓ {rate(last.net_rx_bytes_per_sec)}"
				sub="↑ {rate(last.net_tx_bytes_per_sec)} · stale"
				icon={ArrowDownUp}
			/>
		</div>
	{:else}
		<Card class="mt-4">
			<EmptyState
				icon={Server}
				title="Unavailable"
				description="No metrics have been collected yet. Metrics appear once the agent responds."
			/>
		</Card>
	{/if}
{/if}
