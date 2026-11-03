<script lang="ts">
	// Alerts (GET /monitoring/alerts) with acknowledgement.
	import { BellOff, Check } from '@lucide/svelte';
	import { Badge, Button, Card, DataTable } from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Alert } from '#lib/api/types.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { dateTime, relative, titleCase } from '#lib/format.ts';

	let filter = $state<'firing' | 'all'>('firing');
	const alerts = resource(
		() =>
			api.get<Alert[]>('/monitoring/alerts', {
				status: filter === 'firing' ? 'firing' : undefined
			}),
		{ poll: 30000 }
	);
	let busy = $state<string | null>(null);
	const sevTone = { info: 'info', warning: 'warning', critical: 'danger' } as const;

	async function ack(a: Alert) {
		busy = a.id;
		try {
			await api.post(`/monitoring/alerts/${a.id}/ack`);
			toast.success('Alert acknowledged', titleCase(a.rule));
			await alerts.reload();
		} catch (e) {
			toast.error('Could not acknowledge the alert', errorMessage(e));
		} finally {
			busy = null;
		}
	}
	const columns = [
		{ key: 'severity', label: 'Severity' },
		{ key: 'rule', label: 'Alert' },
		{ key: 'value', label: 'Value' },
		{ key: 'started', label: 'Started' },
		{ key: 'status', label: 'Status' },
		{ key: 'actions', label: 'Acknowledgement', class: 'text-right' }
	];
</script>

<Card padded={false}>
	{#snippet actions()}
		<label class="flex items-center gap-2 text-sm text-fg-muted" for="alert-filter">
			Show
			<select id="alert-filter" class="input h-8 w-auto py-0" bind:value={filter}>
				<option value="firing">Firing only</option>
				<option value="all">All (latest 200)</option>
			</select>
		</label>
	{/snippet}
	<DataTable
		{columns}
		rows={alerts.data ?? []}
		loading={alerts.loading}
		error={alerts.data ? null : alerts.error}
		retry={alerts.reload}
	>
		{#snippet empty()}
			<div class="flex flex-col items-center px-6 py-12 text-center">
				<div class="mb-3 rounded-full bg-success-soft p-3 text-success">
					<BellOff class="size-6" aria-hidden="true" />
				</div>
				<h3 class="text-sm font-semibold">
					{filter === 'firing' ? 'No firing alerts' : 'No alerts recorded'}
				</h3>
				<p class="mt-1 text-sm text-fg-muted">
					Alert rules are evaluated by the worker; thresholds are configured in Platform Settings.
				</p>
			</div>
		{/snippet}
		{#snippet row(a)}
			<tr>
				<td class="px-4 py-3"
					><Badge tone={sevTone[a.severity]} dot>{titleCase(a.severity)}</Badge></td
				>
				<td class="px-4 py-3">
					<p class="font-medium">
						{titleCase(a.rule)}{#if a.subject}<span class="font-mono text-xs text-fg-muted">
								· {a.subject}</span
							>{/if}
					</p>
					<p class="text-xs text-fg-muted">{a.message}</p>
				</td>
				<td class="px-4 py-3 tabular-nums">{a.value === null ? '—' : a.value.toFixed(1)}</td>
				<td class="px-4 py-3 text-xs whitespace-nowrap" title={dateTime(a.started_at)}
					>{relative(a.started_at)}{#if a.resolved_at}<br /><span class="text-fg-muted"
							>resolved {relative(a.resolved_at)}</span
						>{/if}</td
				>
				<td class="px-4 py-3"
					><Badge tone={a.status === 'firing' ? 'danger' : 'success'} dot
						>{titleCase(a.status)}</Badge
					></td
				>
				<td class="px-4 py-3 text-right">
					{#if a.acknowledged_at}
						<span class="text-xs text-fg-muted" title={dateTime(a.acknowledged_at)}
							>by {a.acknowledged_by_username ?? 'unknown'} · {relative(a.acknowledged_at)}</span
						>
					{:else}
						<Button size="sm" loading={busy === a.id} onclick={() => ack(a)}
							><Check class="size-3.5" /> Acknowledge</Button
						>
					{/if}
				</td>
			</tr>
		{/snippet}
	</DataTable>
</Card>
