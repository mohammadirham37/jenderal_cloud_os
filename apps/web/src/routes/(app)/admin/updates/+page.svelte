<script lang="ts">
	import { CircleCheck, CloudDownload, ExternalLink, RefreshCw, RotateCw } from '@lucide/svelte';
	import {
		Alert,
		Badge,
		Button,
		Card,
		ErrorState,
		PageHeader,
		Skeleton,
		Spinner
	} from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import { resource, type Resource } from '#lib/api/resource.svelte.ts';
	import {
		RUNNING_UPDATE_STATES,
		type UpdateJob,
		type UpdateJobState,
		type UpdateStatus
	} from '#lib/api/types-updates.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { dateTime, relative } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import { guarded } from '#lib/components/admin/access.ts';

	const perms = ['system.update', 'host.operations.view'];
	const allowed = $derived(auth.can(...perms));
	const POLL_MS = 2500;

	/** Version an update was started for in this window (until it finishes). */
	let target = $state<string | null>(null);
	let healthVersion = $state<string | null>(null);
	let checking = $state(false);
	let starting = $state(false);

	const status: Resource<UpdateStatus> = resource(
		guarded(perms, () => api.get<UpdateStatus>('/admin/system/update')),
		{ poll: (): number => (watching ? POLL_MS : 0) }
	);
	const s: UpdateStatus | undefined = $derived(status.data);
	const job: UpdateJob | null = $derived(s?.job ?? null);
	const running: boolean = $derived(!!job && RUNNING_UPDATE_STATES.includes(job.state));
	const watchVersion: string | null = $derived(target ?? (running ? (job?.version ?? null) : null));
	const finished: boolean = $derived(
		!!watchVersion &&
			!!job &&
			job.version === watchVersion &&
			(job.state === 'failed' ||
				(job.state === 'succeeded' &&
					(s?.installed_version === watchVersion || healthVersion === watchVersion)))
	);
	const watching: boolean = $derived(!!watchVersion && !finished);
	const succeeded = $derived(finished && job?.state === 'succeeded');
	const failed = $derived(!!job && job.state === 'failed' && job.version === watchVersion);
	/** The API is restarting while an update runs: errors are expected then. */
	const restarting = $derived(watching && !!status.error);
	const canStart = $derived(
		!!s?.update_available && !!s?.can_update && !running && !watching && !!s.latest
	);

	// Probe /health/ready while an update runs; it reports the API version.
	$effect(() => {
		if (!watching) return;
		const t = setInterval(async () => {
			try {
				const res = await fetch('/health/ready', { headers: { Accept: 'application/json' } });
				const body = (await res.json()) as { version?: string };
				if (body?.version) healthVersion = body.version;
			} catch {
				// Expected while services restart.
			}
		}, POLL_MS);
		return () => clearInterval(t);
	});

	let logBox = $state<HTMLPreElement | null>(null);
	$effect(() => {
		void job?.log?.length;
		if (logBox) logBox.scrollTop = logBox.scrollHeight;
	});

	async function checkNow() {
		checking = true;
		try {
			status.set(await api.post<UpdateStatus>('/admin/system/update/check'));
			const st = status.data;
			if (st?.check_error) toast.error('Update check failed', st.check_error);
			else if (st?.update_available) toast.success(`Version ${st.latest?.version} is available`);
			else toast.success('Jenderal Cloud OS is up to date');
		} catch (err) {
			toast.error('Update check failed', errorMessage(err));
		} finally {
			checking = false;
		}
	}

	async function startUpdate() {
		const latest = s?.latest;
		if (!latest) return;
		const phrase = `v${latest.version}`;
		const { confirmed, reason } = await confirmDialog.ask({
			title: `Update to ${phrase}`,
			message: `The server downloads release ${phrase}, verifies its checksum, backs up the platform database and installs it. All platform services restart, so the console and API are unavailable for a few minutes and open terminals disconnect. If the new release fails its health check, the previous release is restored automatically.`,
			reasonLabel: 'Reason (at least 10 characters; recorded in the audit log)',
			reasonRequired: true,
			reasonMinLength: 10,
			requireText: phrase,
			danger: true,
			confirmLabel: `Update to ${phrase}`
		});
		if (!confirmed) return;
		if (reason.length < 10) {
			toast.error('Reason too short', 'Enter a reason of at least 10 characters.');
			return;
		}
		starting = true;
		try {
			const res = await api.post<UpdateStatus>('/admin/system/update/start', {
				version: latest.version,
				confirm: phrase,
				reason
			});
			healthVersion = null;
			target = latest.version;
			if (res) status.set(res);
			toast.success('Update started', `Installing ${phrase}. Keep this window open.`);
		} catch (err) {
			if (err instanceof ApiError && err.status === 422 && Object.keys(err.details).length) {
				toast.error('Update not started', Object.values(err.details).join(' '));
			} else {
				toast.error('Update not started', errorMessage(err));
			}
			status.reload();
		} finally {
			starting = false;
		}
	}

	const steps: { state: UpdateJobState; label: string }[] = [
		{ state: 'queued', label: 'Queued' },
		{ state: 'downloading', label: 'Download' },
		{ state: 'verifying', label: 'Verify' },
		{ state: 'upgrading', label: 'Install & restart' },
		{ state: 'succeeded', label: 'Done' }
	];
	const stepIndex = (st: UpdateJobState | undefined) =>
		st ? steps.findIndex((x) => x.state === st) : -1;

	const stateTone = (st: UpdateJobState) =>
		st === 'succeeded'
			? 'success'
			: st === 'failed'
				? 'danger'
				: RUNNING_UPDATE_STATES.includes(st)
					? 'info'
					: 'neutral';
</script>

<PageHeader
	title="System update"
	description="Compare the installed Jenderal Cloud OS version with the latest published release and update the server."
>
	{#snippet actions()}
		{#if allowed && s}
			<Button onclick={checkNow} loading={checking} disabled={watching}
				><RefreshCw class="size-4" /> Check now</Button
			>
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else if status.error && !s}
	<Card><ErrorState error={status.error} retry={status.reload} /></Card>
{:else if !s}
	<div class="grid gap-6 lg:grid-cols-2">
		{#each [1, 2] as i (i)}<div class="card p-5"><Skeleton lines={5} /></div>{/each}
	</div>
{:else}
	{#if !s.enabled}
		<div class="mb-4">
			<Alert tone="info" title="Automatic update checks are disabled">
				Set <code class="font-mono">UPDATE_CHECK_ENABLED=true</code> in the server configuration to check
				for new releases.
			</Alert>
		</div>
	{/if}
	{#if s.check_error}
		<div class="mb-4">
			<Alert tone="warning" title="Could not check for updates">
				{s.check_error} The server may be offline or GitHub may be unreachable.
			</Alert>
		</div>
	{/if}
	{#if s.job_error}
		<div class="mb-4">
			<Alert tone="warning" title="Update status unavailable">{s.job_error}</Alert>
		</div>
	{/if}

	{#if succeeded}
		<div class="mb-4">
			<Alert tone="success" title="Update completed">
				Jenderal Cloud OS {watchVersion} is now running. Reload the console to load the new version.
				<div class="mt-3">
					<Button variant="primary" size="sm" onclick={() => location.reload()}
						><RotateCw class="size-4" /> Reload console</Button
					>
				</div>
			</Alert>
		</div>
	{:else if failed && job}
		<div class="mb-4">
			<Alert tone="danger" title="Update to v{job.version} failed">
				{job.message || 'The updater reported an error.'}
				If the new release failed its health check, the previous release was restored automatically. See
				<code class="font-mono">/var/log/itk-cloud/update.log</code> on the server for details.
			</Alert>
		</div>
	{/if}

	<div class="grid gap-6 lg:grid-cols-2">
		<Card title="Versions">
			{#snippet actions()}
				{#if s.update_available}
					<Badge tone="accent" dot>Update available</Badge>
				{:else if s.latest}
					<Badge tone="success">Up to date</Badge>
				{/if}
			{/snippet}
			<dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
				<dt class="text-fg-muted">Installed</dt>
				<dd class="font-mono">{s.installed_version || '—'}</dd>
				<dt class="text-fg-muted">Latest release</dt>
				<dd class="font-mono">{s.latest?.version ?? '—'}</dd>
				<dt class="text-fg-muted">Published</dt>
				<dd title={dateTime(s.latest?.published_at)}>
					{s.latest?.published_at ? relative(s.latest.published_at) : '—'}
				</dd>
				<dt class="text-fg-muted">Release package</dt>
				<dd class="font-mono text-xs break-all">
					{#if s.latest}
						{s.latest.asset || '—'}
						{#if !s.latest.asset_available}<Badge tone="warning"
								>Not published for this server</Badge
							>{/if}
					{:else}—{/if}
				</dd>
				<dt class="text-fg-muted">Source</dt>
				<dd class="font-mono text-xs break-all">{s.repo || '—'}</dd>
				<dt class="text-fg-muted">Last checked</dt>
				<dd title={dateTime(s.checked_at)}>{s.checked_at ? relative(s.checked_at) : 'Never'}</dd>
			</dl>
			{#if s.latest?.html_url}
				<a
					href={s.latest.html_url}
					target="_blank"
					rel="noopener noreferrer"
					class="link mt-4 inline-flex items-center gap-1 text-sm"
					>View release on GitHub <ExternalLink class="size-3.5" aria-hidden="true" /></a
				>
			{/if}

			{#if canStart && s.latest}
				<div class="mt-5 border-t border-border pt-4">
					<Button variant="danger" loading={starting} onclick={startUpdate}
						><CloudDownload class="size-4" /> Update to v{s.latest.version}</Button
					>
					<p class="mt-2 text-xs text-fg-muted">
						Services restart during the update. A database backup is taken first and the previous
						release is restored automatically if the health check fails.
					</p>
				</div>
			{:else if s.update_available && !s.can_update}
				<p class="mt-5 border-t border-border pt-4 text-xs text-fg-muted">
					Only a super administrator (permission <code class="font-mono">system.update</code>) can
					install updates.
				</p>
			{/if}
		</Card>

		<Card title="Release notes">
			{#if s.latest?.notes}
				<pre class="max-h-96 overflow-auto font-sans text-sm whitespace-pre-wrap text-fg">{s.latest
						.notes}</pre>
			{:else if s.latest}
				<p class="text-sm text-fg-muted">This release has no notes.</p>
			{:else}
				<p class="text-sm text-fg-muted">
					No release information is available yet. Use “Check now” to query the release server.
				</p>
			{/if}
		</Card>
	</div>

	{#if job && job.state !== 'idle'}
		<div class="mt-6">
			<Card title="Update to v{job.version}">
				{#snippet actions()}
					{#if restarting}
						<span class="inline-flex items-center gap-2 text-xs text-fg-muted"
							><Spinner size={14} /> Services restarting…</span
						>
					{:else if watching}
						<span class="inline-flex items-center gap-2 text-xs text-fg-muted"
							><Spinner size={14} /> In progress</span
						>
					{/if}
					<Badge tone={stateTone(job.state)} dot>{job.state}</Badge>
				{/snippet}

				<ol class="mb-4 flex flex-wrap gap-2 text-xs" aria-label="Update progress">
					{#each steps as step, i (step.state)}
						{@const cur = stepIndex(job.state)}
						{@const done = job.state === 'succeeded' || (cur >= 0 && i < cur)}
						{@const active = job.state !== 'succeeded' && i === cur}
						<li
							class="flex items-center gap-1.5 rounded-full px-2.5 py-1 ring-1 ring-inset {done
								? 'bg-success-soft text-success ring-success/20'
								: active
									? 'bg-info-soft text-info ring-info/20'
									: 'bg-panel-2 text-fg-muted ring-border'}"
							aria-current={active ? 'step' : undefined}
						>
							{#if done}<CircleCheck class="size-3.5" aria-hidden="true" />{:else if active}<Spinner
									size={12}
								/>{/if}
							{step.label}
						</li>
					{/each}
				</ol>

				<dl class="mb-4 grid grid-cols-[auto_1fr] gap-x-6 gap-y-1 text-sm">
					<dt class="text-fg-muted">From</dt>
					<dd class="font-mono">{job.from_version || '—'}</dd>
					<dt class="text-fg-muted">Started</dt>
					<dd>{dateTime(job.started_at)}</dd>
					{#if job.finished_at}
						<dt class="text-fg-muted">Finished</dt>
						<dd>{dateTime(job.finished_at)}</dd>
					{/if}
					{#if job.message && job.state !== 'failed'}
						<dt class="text-fg-muted">Status</dt>
						<dd>{job.message}</dd>
					{/if}
				</dl>

				{#if job.log?.length}
					<pre
						bind:this={logBox}
						class="max-h-80 overflow-auto rounded-md border border-border bg-surface p-3 font-mono text-xs whitespace-pre-wrap text-fg"
						aria-label="Updater log"
						aria-live="polite">{job.log.join('\n')}</pre>
				{:else}
					<p class="text-sm text-fg-muted">No log output yet.</p>
				{/if}
			</Card>
		</div>
	{/if}
{/if}
