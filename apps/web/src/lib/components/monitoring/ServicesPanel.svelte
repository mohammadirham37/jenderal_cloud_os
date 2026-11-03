<script lang="ts">
	// Platform service health, queue statistics and last backup
	// (GET /monitoring/services), polled every 15 s.
	import { ExternalLink } from '@lucide/svelte';
	import {
		Badge,
		Card,
		ErrorState,
		Skeleton,
		StatCard,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { ServicesOverview } from '#lib/api/types-admin.ts';
	import { dateTime, duration, relative, titleCase } from '#lib/format.ts';
	import ServiceList from './ServiceList.svelte';

	const svc = resource(() => api.get<ServicesOverview>('/monitoring/services'), { poll: 15000 });
	const containerCounts = $derived(Object.entries(svc.data?.containers_by_status ?? {}));
</script>

{#if svc.error && !svc.data}
	<Card><ErrorState error={svc.error} retry={svc.reload} /></Card>
{:else if !svc.data}
	<div class="card p-5"><Skeleton lines={6} /></div>
{:else}
	{@const s = svc.data}
	<div class="grid gap-6 lg:grid-cols-3">
		<Card title="Services" class="lg:col-span-2" padded={false}>
			{#snippet actions()}<span class="text-xs text-fg-muted">Refreshes every 15 seconds</span
				>{/snippet}
			<ServiceList services={s.services} />
		</Card>
		<div class="space-y-6">
			<Card title="Last backup">
				{#if !s.last_backup.status}
					<p class="text-sm text-fg-muted">No backup has been run yet.</p>
				{:else}
					<div class="flex items-center justify-between gap-2">
						<span class="text-sm">{titleCase(s.last_backup.kind ?? 'backup')}</span>
						<StatusBadge status={s.last_backup.status} />
					</div>
					<p class="mt-1 text-xs text-fg-muted" title={dateTime(s.last_backup.finished_at)}>
						{relative(s.last_backup.finished_at)}
					</p>
				{/if}
				<a href="/admin/backups" class="link mt-3 inline-block text-sm">Manage backups</a>
			</Card>
			<Card title="Containers by status">
				{#if containerCounts.length === 0}
					<p class="text-sm text-fg-muted">No containers exist yet.</p>
				{:else}
					<ul class="flex flex-wrap gap-2">
						{#each containerCounts as [status, n] (status)}
							<li>
								<Badge
									tone={status === 'running'
										? 'success'
										: status === 'failed'
											? 'danger'
											: 'neutral'}>{titleCase(status)}: {n}</Badge
								>
							</li>
						{/each}
					</ul>
				{/if}
			</Card>
			{#if s.prometheus_url || s.grafana_url}
				<Card title="External dashboards">
					<ul class="space-y-2 text-sm">
						{#if s.prometheus_url}<li>
								<a
									class="link inline-flex items-center gap-1"
									href={s.prometheus_url}
									target="_blank"
									rel="noopener noreferrer"
									>Prometheus <ExternalLink class="size-3.5" aria-hidden="true" /></a
								>
							</li>{/if}
						{#if s.grafana_url}<li>
								<a
									class="link inline-flex items-center gap-1"
									href={s.grafana_url}
									target="_blank"
									rel="noopener noreferrer"
									>Grafana <ExternalLink class="size-3.5" aria-hidden="true" /></a
								>
							</li>{/if}
					</ul>
				</Card>
			{/if}
		</div>
	</div>

	<h2 class="mt-8 mb-3 text-sm font-semibold text-fg">Background job queue</h2>
	<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
		<StatCard
			label="Queued"
			value={String(s.queue.queued)}
			sub={s.queue.queued
				? `oldest waiting ${duration(s.queue.oldest_queued_seconds)}`
				: 'Queue is empty'}
		/>
		<StatCard label="Running" value={String(s.queue.running)} />
		<StatCard label="Failed (24 h)" value={String(s.queue.failed_24h)} />
		<StatCard label="Dead (total)" value={String(s.queue.dead)} sub="Exhausted all retries" />
		<a href="/monitoring?tab=jobs" class="card link flex items-center justify-center p-4 text-sm"
			>Inspect jobs</a
		>
	</div>
{/if}
