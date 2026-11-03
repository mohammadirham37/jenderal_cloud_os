<script lang="ts">
	import { tick, untrack } from 'svelte';
	import { Play, RefreshCw, Square, Trash2 } from '@lucide/svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { ContainerDetail, LogsResult } from '#lib/api/types-workspaces.ts';
	import {
		Alert,
		Badge,
		Button,
		Card,
		ErrorState,
		Meter,
		PageHeader,
		Skeleton,
		StatusBadge,
		Switch
	} from '#lib/components/ui/index.ts';
	import HistoryChart from '#lib/components/workspaces/HistoryChart.svelte';
	import {
		TRANSIENT_CTR,
		containerAction,
		toastError,
		withAccessReason
	} from '#lib/components/workspaces/workspace.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { crumbs } from '#lib/stores/breadcrumb.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes, cores, dateTime, percent, relative } from '#lib/format.ts';

	const id = $derived(page.params.id ?? '');
	const detail = resource(() => api.get<ContainerDetail>(`/containers/${id}`));
	const c = $derived(detail.data?.id === id ? detail.data : undefined);

	const fast = $derived(!!c && (c.pending_job !== null || TRANSIENT_CTR.includes(c.status)));
	$effect(() => {
		const ms = fast ? 3000 : 15000;
		const t = setInterval(() => {
			if (document.visibilityState === 'visible') untrack(() => detail.reload());
		}, ms);
		return () => clearInterval(t);
	});

	let removing = $state(false);
	$effect(() => {
		if (c) {
			crumbs.set(c.id, c.name);
			if (c.status === 'removing') removing = true;
		}
	});
	const notFound = $derived(detail.error instanceof ApiError && detail.error.status === 404);
	let lastWorkspace = $state('');
	$effect(() => {
		if (c) lastWorkspace = c.workspace_id;
	});
	$effect(() => {
		if (notFound && removing) {
			toast.success('Container removed');
			goto(lastWorkspace ? `/workspaces/${lastWorkspace}?tab=containers` : '/containers');
		}
	});

	const canAct = $derived(auth.can('containers.stop', 'containers.manage_all'));
	const idle = $derived(!!c && c.pending_job === null && !TRANSIENT_CTR.includes(c.status));
	let acting = $state<string | null>(null);

	async function act(action: 'start' | 'stop' | 'restart') {
		if (!c) return;
		acting = action;
		const out = await containerAction(c, action);
		acting = null;
		if (out && c) {
			detail.set({ ...c, ...out });
			detail.reload();
		}
	}

	async function remove() {
		if (!c) return;
		const r = await confirmDialog.ask({
			title: `Delete container ${c.name}?`,
			message:
				'The container is removed. Files in the shared workspace volume are kept, but anything stored only inside this container is lost.',
			requireText: c.name,
			confirmLabel: 'Delete container',
			danger: true
		});
		if (!r.confirmed) return;
		acting = 'delete';
		try {
			const res = await withAccessReason((headers) =>
				api.del(`/containers/${c!.id}`, undefined, { headers }).then(() => true)
			);
			if (res) {
				removing = true;
				toast.success('Removing container', c.name);
				detail.reload();
			}
		} catch (e) {
			toastError(`Could not delete ${c.name}`, e);
		} finally {
			acting = null;
		}
	}

	// Logs.
	let tail = $state(200);
	let autoRefresh = $state(false);
	let pre = $state<HTMLPreElement>();
	const logs = resource(() =>
		api.get<LogsResult>(`/containers/${id}/logs`, { tail, timestamps: 'true' })
	);
	$effect(() => {
		if (!autoRefresh) return;
		const t = setInterval(() => {
			if (document.visibilityState === 'visible') untrack(() => logs.reload());
		}, 5000);
		return () => clearInterval(t);
	});
	$effect(() => {
		if (!logs.data || !pre) return;
		const el = pre;
		const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
		const first = el.scrollTop === 0;
		tick().then(() => {
			if (atBottom || first) el.scrollTop = el.scrollHeight;
		});
	});
	const logsError = $derived(
		logs.error instanceof ApiError && logs.error.status === 403
			? 'You need operator access to this workspace to read container logs.'
			: logs.error
				? errorMessage(logs.error)
				: ''
	);
	const hostPorts = $derived(Object.entries(c?.runtime?.host_ports ?? {}));
</script>

{#if detail.error && !(notFound && removing)}
	<PageHeader title="Container" />
	<Card><ErrorState error={detail.error} retry={detail.reload} /></Card>
{:else if !c}
	<PageHeader title="Container" />
	<Card><Skeleton lines={6} /></Card>
{:else}
	<PageHeader title={c.name}>
		{#snippet meta()}
			<StatusBadge status={c.status} pending={c.pending_job} />
			{#if c.is_primary}<Badge tone="accent">primary</Badge>{/if}
			<span
				>Workspace <a href="/workspaces/{c.workspace_id}" class="link">{c.workspace_name}</a></span
			>
			<span aria-hidden="true">·</span>
			<span>Owner <strong class="text-fg">{c.owner_username}</strong></span>
		{/snippet}
		{#snippet actions()}
			{#if canAct && c.status === 'running'}
				<Button
					disabled={!idle || !!acting}
					loading={acting === 'restart'}
					onclick={() => act('restart')}><RefreshCw class="size-4" /> Restart</Button
				>
				<Button disabled={!idle || !!acting} loading={acting === 'stop'} onclick={() => act('stop')}
					><Square class="size-4" /> Stop</Button
				>
			{:else if canAct && ['created', 'stopped', 'failed'].includes(c.status)}
				<Button
					variant="primary"
					disabled={!idle || !!acting}
					loading={acting === 'start'}
					onclick={() => act('start')}><Play class="size-4" /> Start</Button
				>
			{/if}
			{#if !c.is_primary && c.status !== 'removing'}
				<Button
					variant="danger"
					disabled={!!acting || c.pending_job !== null}
					loading={acting === 'delete'}
					onclick={remove}><Trash2 class="size-4" /> Delete</Button
				>
			{/if}
		{/snippet}
	</PageHeader>

	{#if c.status_reason}
		<div class="mb-4">
			<Alert tone={c.status === 'failed' ? 'danger' : 'info'}>{c.status_reason}</Alert>
		</div>
	{/if}
	{#if c.is_primary}
		<p class="mb-4 text-xs text-fg-muted">
			This is the workspace's primary container; it is removed together with the workspace.
		</p>
	{/if}

	<div class="grid gap-6 lg:grid-cols-3">
		<Card title="Configuration">
			<dl class="space-y-2 text-sm">
				<div class="flex justify-between gap-3">
					<dt class="text-fg-muted">Image</dt>
					<dd class="truncate font-mono text-xs" title={c.image}>{c.image}</dd>
				</div>
				<div class="flex justify-between gap-3">
					<dt class="text-fg-muted">Template</dt>
					<dd>{c.template_name}</dd>
				</div>
				<div class="flex justify-between gap-3">
					<dt class="text-fg-muted">CPU limit</dt>
					<dd>{cores(c.cpu_millis)}</dd>
				</div>
				<div class="flex justify-between gap-3">
					<dt class="text-fg-muted">Memory limit</dt>
					<dd>{bytes(c.memory_bytes)}</dd>
				</div>
				<div class="flex justify-between gap-3">
					<dt class="text-fg-muted">Process limit</dt>
					<dd>{c.pids_limit}</dd>
				</div>
				<div class="flex justify-between gap-3">
					<dt class="text-fg-muted">Ports</dt>
					<dd>
						{c.ports.length
							? c.ports.map((p) => `${p.port}${p.name ? ` (${p.name})` : ''}`).join(', ')
							: '—'}
					</dd>
				</div>
				<div class="flex justify-between gap-3">
					<dt class="text-fg-muted">GPUs</dt>
					<dd>{c.gpu_device_ids.length ? c.gpu_device_ids.length : 'None'}</dd>
				</div>
				<div class="flex justify-between gap-3">
					<dt class="text-fg-muted">Created</dt>
					<dd>{dateTime(c.created_at)}</dd>
				</div>
			</dl>
		</Card>

		<Card title="Runtime">
			{#if c.runtime_error}
				<p class="text-sm text-fg-muted">
					<span class="font-medium text-fg">Unavailable.</span>
					{c.runtime_error} — the host agent did not answer.
				</p>
			{:else if !c.runtime}
				<p class="text-sm text-fg-muted">
					The container has not been created on the host yet{c.status === 'stopped'
						? ' (it is recreated on the next start)'
						: ''}.
				</p>
			{:else}
				<dl class="space-y-2 text-sm">
					<div class="flex justify-between gap-3">
						<dt class="text-fg-muted">Engine state</dt>
						<dd><StatusBadge status={c.runtime.status} /></dd>
					</div>
					<div class="flex justify-between gap-3">
						<dt class="text-fg-muted">Started</dt>
						<dd title={dateTime(c.runtime.started_at)}>{relative(c.runtime.started_at)}</dd>
					</div>
					{#if c.runtime.status !== 'running'}
						<div class="flex justify-between gap-3">
							<dt class="text-fg-muted">Exit code</dt>
							<dd class="tabular-nums">{c.runtime.exit_code}</dd>
						</div>
					{/if}
					<div class="flex justify-between gap-3">
						<dt class="text-fg-muted">Killed (out of memory)</dt>
						<dd class={c.runtime.oom_killed ? 'font-medium text-danger' : ''}>
							{c.runtime.oom_killed ? 'Yes' : 'No'}
						</dd>
					</div>
					<div class="flex justify-between gap-3">
						<dt class="text-fg-muted">Published ports</dt>
						<dd class="font-mono text-xs">
							{hostPorts.length
								? hostPorts.map(([p, h]) => `${p} → 127.0.0.1:${h}`).join(', ')
								: '—'}
						</dd>
					</div>
					{#if c.runtime.error}
						<p class="text-xs text-danger">{c.runtime.error}</p>
					{/if}
				</dl>
				{#if c.runtime.oom_killed}
					<div class="mt-3">
						<Alert tone="warning"
							>The last run was stopped because it ran out of memory. Consider a higher memory
							limit.</Alert
						>
					</div>
				{/if}
			{/if}
		</Card>

		<Card title="Live usage">
			{#if c.status !== 'running'}
				<p class="text-sm text-fg-muted">Live usage is shown while the container is running.</p>
			{:else if !c.stats}
				<p class="text-sm text-fg-muted">
					<span class="font-medium text-fg">Unavailable.</span> The host agent did not return statistics.
				</p>
			{:else}
				<div class="space-y-3">
					<Meter
						label="CPU ({percent(c.stats.cpu_percent)} of one core)"
						value={c.stats.cpu_percent}
						max={(c.cpu_millis / 1000) * 100}
						display="{(c.stats.cpu_percent / 100).toFixed(2)} of {cores(c.cpu_millis)}"
					/>
					<Meter
						label="Memory"
						value={c.stats.memory_bytes}
						max={c.stats.memory_limit}
						display="{bytes(c.stats.memory_bytes)} of {bytes(c.stats.memory_limit)}"
					/>
					<p class="text-xs text-fg-muted">
						{c.stats.pids} processes · {bytes(c.stats.net_rx_bytes)} received · {bytes(
							c.stats.net_tx_bytes
						)} sent
					</p>
				</div>
			{/if}
		</Card>
	</div>

	<div class="mt-6">
		<Card title="Logs" description="Most recent output of the container (stdout and stderr).">
			{#snippet actions()}
				<label class="sr-only" for="log-tail">Lines</label>
				<select id="log-tail" class="input h-8 w-auto py-0 text-xs" bind:value={tail}>
					<option value={200}>Last 200 lines</option>
					<option value={500}>Last 500 lines</option>
					<option value={1000}>Last 1000 lines</option>
				</select>
				<div class="flex items-center gap-2 text-xs text-fg-muted">
					<Switch id="log-auto" label="Auto-refresh every 5 seconds" bind:checked={autoRefresh} />
					<label for="log-auto">Auto-refresh</label>
				</div>
				<Button size="sm" loading={logs.loading} onclick={logs.reload}
					><RefreshCw class="size-3.5" /> Refresh</Button
				>
			{/snippet}
			{#if logsError}
				<p class="text-sm text-fg-muted" role="alert">{logsError}</p>
			{:else if !logs.data}
				<Skeleton lines={6} />
			{:else}
				{#if logs.data.truncated}
					<p class="mb-2 text-xs text-warning">
						Output was truncated; only the most recent part is shown.
					</p>
				{/if}
				<pre
					bind:this={pre}
					class="max-h-[28rem] min-h-32 overflow-auto rounded-md border border-border bg-surface p-3 font-mono text-xs leading-relaxed whitespace-pre-wrap text-fg"
					aria-label="Container logs">{logs.data.lines.length
						? logs.data.lines.join('\n')
						: 'No log output yet.'}</pre>
			{/if}
		</Card>
	</div>

	<div class="mt-6">
		<HistoryChart scope="container" ref={c.id} />
	</div>
{/if}
