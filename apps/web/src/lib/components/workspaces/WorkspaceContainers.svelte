<script lang="ts">
	// Containers tab of the workspace detail page.
	import { Boxes, ExternalLink, Plus } from '@lucide/svelte';
	import type { Container, WorkspaceDetail } from '#lib/api/types.ts';
	import {
		Badge,
		Button,
		Card,
		DataTable,
		EmptyState,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { appConfig } from '#lib/stores/config.svelte.ts';
	import { bytes, cores, dateTime, relative } from '#lib/format.ts';
	import AddContainerModal from './AddContainerModal.svelte';

	let { ws, onchange }: { ws: WorkspaceDetail; onchange: () => void } = $props();

	let adding = $state(false);

	const proxyOrigin = $derived((appConfig.value?.app_proxy_origin ?? '').replace(/\/+$/, ''));
	const canAdd = $derived(
		auth.can('containers.create') &&
			ws.grants.Edit &&
			ws.type === 'container' &&
			['ready', 'running', 'stopped'].includes(ws.status)
	);
	const hasPublished = $derived(
		ws.containers.some((c) => c.status === 'running' && c.ports.some((p) => p.host_port))
	);

	const columns = [
		{ key: 'name', label: 'Name' },
		{ key: 'image', label: 'Image' },
		{ key: 'status', label: 'Status' },
		{ key: 'resources', label: 'CPU / memory' },
		{ key: 'started', label: 'Started' },
		{ key: 'ports', label: 'Ports' }
	];

	function appUrl(port: number): string {
		return `${proxyOrigin}/api/v1/workspaces/${ws.id}/proxy/${port}/`;
	}
</script>

<Card
	title="Containers"
	description={ws.type === 'native'
		? 'Linux-native workspaces run directly on the host and have no containers.'
		: 'The primary container is created with the workspace; extra containers share its volume and network.'}
	padded={false}
>
	{#snippet actions()}
		{#if canAdd}
			<Button size="sm" onclick={() => (adding = true)}
				><Plus class="size-3.5" /> Add container</Button
			>
		{/if}
	{/snippet}
	{#if hasPublished && !proxyOrigin}
		<p class="border-b border-border px-5 py-2 text-xs text-fg-muted">
			The app proxy is not configured on this server, so published ports cannot be opened from the
			browser. Ask an administrator to set APP_PROXY_ORIGIN.
		</p>
	{/if}
	<DataTable {columns} rows={ws.containers}>
		{#snippet row(c: Container)}
			<tr class="hover:bg-panel-2/40">
				<td class="px-4 py-2.5">
					<a href="/containers/{c.id}" class="font-medium hover:underline">{c.name}</a>
					{#if c.is_primary}<span class="ml-1"><Badge tone="accent">primary</Badge></span>{/if}
				</td>
				<td class="max-w-56 truncate px-4 py-2.5 font-mono text-xs text-fg-muted" title={c.image}>
					{c.image}
				</td>
				<td class="px-4 py-2.5">
					<StatusBadge status={c.status} pending={c.pending_job} />
					{#if c.status_reason && c.status === 'failed'}
						<p class="mt-0.5 max-w-56 truncate text-xs text-danger" title={c.status_reason}>
							{c.status_reason}
						</p>
					{/if}
				</td>
				<td class="px-4 py-2.5 text-xs whitespace-nowrap text-fg-muted tabular-nums">
					{cores(c.cpu_millis)} · {bytes(c.memory_bytes)}
				</td>
				<td
					class="px-4 py-2.5 text-xs whitespace-nowrap text-fg-muted"
					title={dateTime(c.started_at)}
				>
					{c.started_at && c.status === 'running' ? relative(c.started_at) : '—'}
				</td>
				<td class="px-4 py-2.5">
					{#if c.ports.length === 0}
						<span class="text-fg-subtle">—</span>
					{:else}
						<ul class="space-y-1">
							{#each c.ports as p (p.port)}
								<li class="flex items-center gap-2 text-xs">
									<span class="font-mono">{p.port}</span>
									{#if p.name}<span class="text-fg-muted">{p.name}</span>{/if}
									{#if p.host_port && c.status === 'running' && proxyOrigin && ws.grants.Edit}
										<a
											href={appUrl(p.port)}
											target="_blank"
											rel="noopener noreferrer"
											class="link inline-flex items-center gap-1"
											>Open app <ExternalLink class="size-3" aria-hidden="true" /><span
												class="sr-only">(opens in a new tab)</span
											></a
										>
									{/if}
								</li>
							{/each}
						</ul>
					{/if}
				</td>
			</tr>
		{/snippet}
		{#snippet empty()}
			<EmptyState
				icon={Boxes}
				title="No containers"
				description={ws.type === 'native'
					? 'This is a Linux-native workspace.'
					: 'Containers appear here once the workspace has been provisioned.'}
			/>
		{/snippet}
	</DataTable>
</Card>

{#if canAdd}
	<AddContainerModal bind:open={adding} workspace={ws} oncreated={onchange} />
{/if}
