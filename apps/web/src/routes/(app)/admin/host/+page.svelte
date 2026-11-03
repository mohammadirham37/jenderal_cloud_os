<script lang="ts">
	import { OctagonX, RefreshCw } from '@lucide/svelte';
	import {
		Alert,
		Badge,
		Button,
		Card,
		ErrorState,
		PageHeader,
		Skeleton,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type {
		AgentStatusResponse,
		EmergencyStopResult,
		ServicesOverview
	} from '#lib/api/types-admin.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes, dateTime, relative } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import ServiceList from '#lib/components/monitoring/ServiceList.svelte';
	import { guarded } from '#lib/components/admin/access.ts';

	const CONFIRM_PHRASE = 'STOP ALL WORKLOADS';
	const perms = ['host.operations.view', 'agent.view'];
	const allowed = $derived(auth.can(...perms));
	const status = resource(
		guarded(perms, () => api.get<AgentStatusResponse>('/admin/agent/status')),
		{ poll: 30000 }
	);
	const services = resource(
		guarded(['monitoring.view_host', 'host.operations.view'], () =>
			api.get<ServicesOverview>('/monitoring/services')
		),
		{ poll: 30000 }
	);
	const node = $derived(status.data?.node);
	const protocolMismatch = $derived(
		!!node &&
			node.protocol_version !== null &&
			node.protocol_version !== node.expected_protocol_version
	);

	let stopping = $state(false);
	let stopResult = $state<(EmergencyStopResult & { at: string }) | null>(null);

	async function emergencyStop() {
		const { confirmed, reason } = await confirmDialog.ask({
			title: 'Emergency stop of all workloads',
			message:
				'Every running workspace container on this host is stopped immediately, all terminal sessions are closed and every user is notified. Data is kept, but unsaved work in running processes is lost. Use this only in an emergency (security incident, overheating, disk full).',
			reasonLabel:
				'Reason (at least 10 characters; sent to all users and recorded in the audit log)',
			reasonRequired: true,
			requireText: CONFIRM_PHRASE,
			danger: true,
			confirmLabel: 'Stop all workloads'
		});
		if (!confirmed) return;
		if (reason.length < 10) {
			toast.error('Reason too short', 'Enter a reason of at least 10 characters.');
			return;
		}
		stopping = true;
		try {
			const res = await api.post<EmergencyStopResult>('/admin/host/emergency-stop', {
				confirm: CONFIRM_PHRASE,
				reason
			});
			stopResult = { ...(res ?? {}), at: new Date().toISOString() };
			toast.success('Emergency stop completed', `${res?.stopped ?? 0} container(s) stopped.`);
			services.reload();
		} catch (err) {
			toast.error('Emergency stop failed', errorMessage(err));
		} finally {
			stopping = false;
		}
	}

	const yes = (v: boolean | undefined) => (v ? 'Yes' : 'No');
</script>

<PageHeader
	title="Host & Agent"
	description="Status and capabilities of the host agent that runs workloads on this server."
>
	{#snippet actions()}
		{#if allowed}
			<Button onclick={status.reload} loading={status.loading && !!status.data}
				><RefreshCw class="size-4" /> Check now</Button
			>
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else if status.error && !status.data}
	<Card><ErrorState error={status.error} retry={status.reload} /></Card>
{:else if !status.data}
	<div class="grid gap-6 lg:grid-cols-2">
		{#each [1, 2, 3, 4] as i (i)}<div class="card p-5"><Skeleton lines={5} /></div>{/each}
	</div>
{:else if !node}
	<Alert tone="danger" title="The agent has never reported in">
		{status.data.error ?? 'No agent status is available.'} Make sure the agent service (<code
			class="font-mono">itk-cloud-agent</code
		>) is installed and running, and that the API can reach its socket. Expected protocol version: {status
			.data.expected_protocol_version ?? '—'}.
	</Alert>
{:else}
	{#if status.data.live_error}
		<div class="mb-4">
			<Alert tone="danger" title="Agent did not respond">
				{status.data.live_error} The information below is the last state the agent reported.
			</Alert>
		</div>
	{/if}
	{#if protocolMismatch}
		<div class="mb-4">
			<Alert tone="warning" title="Protocol version mismatch">
				The agent speaks protocol {node.protocol_version} but the API expects {node.expected_protocol_version}.
				Upgrade the agent and API to the same release.
			</Alert>
		</div>
	{/if}
	{#if !node.capabilities.storage_enforced}
		<div class="mb-4">
			<Alert tone="warning" title="Storage quotas are not enforced">
				The storage driver “{node.capabilities.storage_driver || 'unknown'}” cannot enforce
				per-workspace disk limits. A single workspace can fill the disk. Use the loopback storage
				driver in production.
			</Alert>
		</div>
	{/if}

	<div class="grid gap-6 lg:grid-cols-2">
		<Card title="Agent">
			{#snippet actions()}<StatusBadge status={node.status} />{/snippet}
			<dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
				<dt class="text-fg-muted">Version</dt>
				<dd class="font-mono">{node.agent_version ?? '—'}</dd>
				<dt class="text-fg-muted">Protocol</dt>
				<dd>
					{node.protocol_version ?? '—'}
					<span class="text-fg-muted">(expected {node.expected_protocol_version})</span>
					{#if protocolMismatch}<Badge tone="warning">Mismatch</Badge>{/if}
				</dd>
				<dt class="text-fg-muted">Hostname</dt>
				<dd class="font-mono">{node.hostname ?? '—'}</dd>
				<dt class="text-fg-muted">Operating system</dt>
				<dd>{node.os ?? '—'}</dd>
				<dt class="text-fg-muted">Kernel</dt>
				<dd class="font-mono">{node.kernel ?? '—'}</dd>
				<dt class="text-fg-muted">Architecture</dt>
				<dd>{node.arch ?? '—'}</dd>
				<dt class="text-fg-muted">Last seen</dt>
				<dd title={dateTime(node.last_seen_at)}>
					{node.last_seen_at ? relative(node.last_seen_at) : 'Never'}
				</dd>
				{#if node.last_error}
					<dt class="text-fg-muted">Last error</dt>
					<dd class="break-words text-danger">{node.last_error}</dd>
				{/if}
			</dl>
		</Card>

		<Card title="Host hardware">
			<dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
				<dt class="text-fg-muted">CPU model</dt>
				<dd>{node.host_info?.cpu_model || '—'}</dd>
				<dt class="text-fg-muted">Cores / threads</dt>
				<dd>{node.host_info?.cpu_cores || '—'} / {node.host_info?.cpu_threads || '—'}</dd>
				<dt class="text-fg-muted">Memory</dt>
				<dd>{node.host_info?.memory_total ? bytes(node.host_info.memory_total) : '—'}</dd>
				<dt class="text-fg-muted">Boot time</dt>
				<dd>{dateTime(node.host_info?.boot_time)}</dd>
			</dl>
			{#if auth.can('monitoring.view_host')}
				<a href="/monitoring" class="link mt-4 inline-block text-sm">Live metrics</a>
			{/if}
		</Card>

		<Card title="Capabilities">
			{@const c = node.capabilities}
			<dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
				<dt class="text-fg-muted">Docker</dt>
				<dd>
					{#if c.docker}<Badge tone="success">Available</Badge>
						<span class="font-mono text-xs">{c.docker_version ?? ''}</span>{:else}<Badge
							tone="danger">Unavailable</Badge
						>
						{#if c.docker_error}<span class="block text-xs break-words text-danger"
								>{c.docker_error}</span
							>{/if}{/if}
				</dd>
				<dt class="text-fg-muted">NVIDIA driver</dt>
				<dd>
					{#if c.nvidia}<Badge tone="success">Detected</Badge>{:else}<Badge>Not detected</Badge
						>{/if}
				</dd>
				<dt class="text-fg-muted">NVIDIA container runtime</dt>
				<dd>
					{#if c.nvidia_runtime}<Badge tone="success">Installed</Badge>{:else}<Badge
							tone={c.nvidia ? 'warning' : 'neutral'}>Not installed</Badge
						>{/if}
				</dd>
				<dt class="text-fg-muted">Storage driver</dt>
				<dd>
					<span class="font-mono">{c.storage_driver || '—'}</span>
					{#if c.storage_enforced}<Badge tone="success">Quota enforced</Badge>{:else}<Badge
							tone="warning">Quota not enforced</Badge
						>{/if}
				</dd>
				<dt class="text-fg-muted">Native workspaces</dt>
				<dd>{yes(c.native_workspaces)}</dd>
				<dt class="text-fg-muted">AppArmor</dt>
				<dd>
					{#if c.apparmor}<Badge tone="success">Enabled</Badge>{:else}<Badge tone="warning"
							>Disabled</Badge
						>{/if}
				</dd>
				<dt class="text-fg-muted">Seccomp</dt>
				<dd>
					{#if c.seccomp}<Badge tone="success">Enabled</Badge>{:else}<Badge tone="warning"
							>Disabled</Badge
						>{/if}
				</dd>
			</dl>
		</Card>

		{#if auth.can('monitoring.view_host', 'host.operations.view')}
			<Card title="Platform services" padded={false}>
				{#if services.error && !services.data}
					<ErrorState error={services.error} retry={services.reload} />
				{:else if !services.data}
					<div class="p-5"><Skeleton lines={5} /></div>
				{:else}
					<ServiceList services={services.data.services} />
					<p class="border-t border-border px-5 py-3 text-xs text-fg-muted">
						Queue: {services.data.queue.queued} queued · {services.data.queue.running} running · {services
							.data.queue.dead} dead
					</p>
				{/if}
			</Card>
		{/if}
	</div>
{/if}

{#if allowed && auth.can('host.operations.execute')}
	<section class="mt-8 rounded-lg border border-danger/40 bg-danger-soft/40 p-5">
		<div class="flex flex-wrap items-start justify-between gap-4">
			<div class="max-w-2xl">
				<h2 class="flex items-center gap-2 text-sm font-semibold text-danger">
					<OctagonX class="size-4" aria-hidden="true" /> Emergency stop
				</h2>
				<p class="mt-1 text-sm text-fg-muted">
					Stops every running workspace container on the host, closes all terminal sessions and
					notifies all users. Requires typing <span class="kbd">{CONFIRM_PHRASE}</span> and a reason.
					The action is audited.
				</p>
			</div>
			<Button variant="danger" loading={stopping} onclick={emergencyStop}
				><OctagonX class="size-4" /> Stop all workloads</Button
			>
		</div>
		{#if stopResult}
			<div class="mt-4">
				<Alert tone={stopResult.failed ? 'warning' : 'success'} title="Emergency stop completed">
					{stopResult.stopped ?? 0} container(s) stopped, {stopResult.failed ?? 0} failed to stop ({dateTime(
						stopResult.at
					)}).{#if stopResult.failed}
						Check the remaining containers on the host.{/if}
				</Alert>
			</div>
		{/if}
	</section>
{/if}
