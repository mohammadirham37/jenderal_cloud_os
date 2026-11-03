<script lang="ts">
	// Health of platform components (GET /monitoring/services).
	import { StatusBadge } from '#lib/components/ui/index.ts';
	import type { ServiceStatus } from '#lib/api/types-admin.ts';
	import { titleCase } from '#lib/format.ts';

	let { services }: { services: ServiceStatus[] } = $props();
	const labels: Record<string, string> = {
		api: 'API server',
		postgresql: 'PostgreSQL',
		redis: 'Redis',
		worker: 'Background worker',
		agent: 'Host agent',
		web: 'Web console'
	};
</script>

<ul class="divide-y divide-border">
	{#each services as s (s.name)}
		<li class="flex items-center justify-between gap-3 px-5 py-3">
			<div class="min-w-0">
				<p class="text-sm font-medium text-fg">{labels[s.name] ?? titleCase(s.name)}</p>
				{#if s.detail}<p class="truncate text-xs text-fg-muted" title={s.detail}>{s.detail}</p>{/if}
			</div>
			<div class="flex shrink-0 items-center gap-3">
				{#if s.latency_ms !== undefined}
					<span class="text-xs text-fg-muted tabular-nums">{s.latency_ms} ms</span>
				{/if}
				<StatusBadge status={s.status} />
			</div>
		</li>
	{/each}
</ul>
