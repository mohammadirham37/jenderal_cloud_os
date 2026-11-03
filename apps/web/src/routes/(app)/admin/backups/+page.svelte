<script lang="ts">
	import { Archive, ArchiveRestore, Plus } from '@lucide/svelte';
	import {
		Alert,
		Badge,
		Button,
		Card,
		CopyButton,
		DataTable,
		EmptyState,
		Field,
		Modal,
		PageHeader,
		Pagination,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { api, errorMessage, type ListMeta } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { BackupJob, Database } from '#lib/api/types.ts';
	import type { BackupConfig, BackupKind } from '#lib/api/types-admin.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes, dateTime, relative, titleCase } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import { guarded } from '#lib/components/admin/access.ts';
	import { handleFormError } from '#lib/components/admin/forms.ts';

	interface BackupList {
		data: BackupJob[];
		meta: ListMeta;
		config: BackupConfig;
	}

	const perPage = 20;
	let pageNo = $state(1);
	const allowed = $derived(auth.can('backup.manage'));
	const backups = resource(
		guarded(['backup.manage'], () =>
			api.raw<BackupList>('GET', '/admin/backups', { query: { page: pageNo, per_page: perPage } })
		)
	);
	const cfg = $derived(backups.data?.config);

	// Poll every 3 s while a backup or restore is queued/running.
	const pending = $derived(
		(backups.data?.data ?? []).some(
			(b) =>
				b.status === 'queued' ||
				b.status === 'running' ||
				b.restore_status === 'queued' ||
				b.restore_status === 'running'
		)
	);
	$effect(() => {
		if (!pending) return;
		const t = setInterval(() => {
			if (document.visibilityState === 'visible') backups.reload();
		}, 3000);
		return () => clearInterval(t);
	});

	const kinds: { id: BackupKind | 'full'; label: string; help: string }[] = [
		{
			id: 'full',
			label: 'Full',
			help: 'Platform database, configuration, workspace metadata, plus workspace files and project databases when enabled.'
		},
		{
			id: 'platform_db',
			label: 'Platform database',
			help: 'Users, roles, workspaces, audit log, settings.'
		},
		{
			id: 'config',
			label: 'Configuration',
			help: 'Server configuration files (secrets are encrypted).'
		},
		{
			id: 'workspace_metadata',
			label: 'Workspace metadata',
			help: 'Workspace and container definitions.'
		},
		{
			id: 'workspace_files',
			label: 'Workspace files',
			help: 'The files inside every workspace. Can be large.'
		},
		{
			id: 'project_databases',
			label: 'Project databases',
			help: 'Dumps of every project PostgreSQL database.'
		}
	];

	// Create
	let createOpen = $state(false);
	let kind = $state<BackupKind | 'full'>('full');
	let creating = $state(false);
	let createErrors = $state<Record<string, string>>({});
	async function create(e: SubmitEvent) {
		e.preventDefault();
		createErrors = {};
		creating = true;
		try {
			const res = await api.post<{ backup_ids: string[] }>('/admin/backups', { kind });
			toast.success(
				'Backup queued',
				`${res.backup_ids.length} job(s) created. Progress updates automatically.`
			);
			createOpen = false;
			pageNo = 1;
			backups.reload();
		} catch (err) {
			createErrors = handleFormError(err, 'Could not start the backup');
		} finally {
			creating = false;
		}
	}

	// Restore
	let restoreOpen = $state(false);
	let target = $state<BackupJob | null>(null);
	let restoring = $state(false);
	let rErrors = $state<Record<string, string>>({});
	let rForm = $state({
		mode: 'verify' as 'verify' | 'restore',
		age_identity: '',
		reason: '',
		confirm: '',
		workspace_id: '',
		database_id: ''
	});
	const verifyOnly = (k: string) => ['platform_db', 'config', 'workspace_metadata'].includes(k);
	const databases = resource(() =>
		restoreOpen && target?.kind === 'project_databases'
			? api.list<Database>('/databases', { per_page: 100 })
			: Promise.resolve(null)
	);

	function openRestore(b: BackupJob) {
		target = b;
		rErrors = {};
		rForm = {
			mode: verifyOnly(b.kind) ? 'verify' : 'restore',
			age_identity: '',
			reason: '',
			confirm: '',
			workspace_id: '',
			database_id: ''
		};
		restoreOpen = true;
	}
	function closeRestore() {
		restoreOpen = false;
		rForm.age_identity = '';
		target = null;
	}

	async function restore(e: SubmitEvent) {
		e.preventDefault();
		const b = target;
		if (!b) return;
		rErrors = {};
		const local: Record<string, string> = {};
		if (!rForm.age_identity.trim().startsWith('AGE-SECRET-KEY-'))
			local.age_identity = 'Paste the age private key (starts with AGE-SECRET-KEY-).';
		if (rForm.reason.trim().length < 5) local.reason = 'Enter a reason (at least 5 characters).';
		if (rForm.confirm !== b.file_name) local.confirm = 'Type the backup file name exactly.';
		if (b.kind === 'project_databases' && rForm.mode === 'restore' && !rForm.database_id)
			local.database_id = 'Select the project database to restore.';
		if (Object.keys(local).length) {
			rErrors = local;
			return;
		}
		restoring = true;
		const body: Record<string, unknown> = {
			confirm: rForm.confirm,
			age_identity: rForm.age_identity.trim(),
			reason: rForm.reason.trim(),
			mode: rForm.mode
		};
		if (b.kind === 'workspace_files' && rForm.workspace_id.trim())
			body.workspace_id = rForm.workspace_id.trim();
		if (b.kind === 'project_databases' && rForm.database_id) body.database_id = rForm.database_id;
		// The private key never stays in component state after submit.
		rForm.age_identity = '';
		try {
			await api.post<BackupJob>(`/admin/backups/${b.id}/restore`, body);
			toast.success(
				rForm.mode === 'verify' ? 'Verification queued' : 'Restore queued',
				'Progress updates automatically in the table.'
			);
			closeRestore();
			backups.reload();
		} catch (err) {
			const fe = handleFormError(err, 'Could not start the restore');
			rErrors = {
				...fe,
				age_identity: fe.age_identity ?? 'The key was cleared for safety. Paste it again to retry.'
			};
		} finally {
			body.age_identity = '';
			restoring = false;
		}
	}

	const columns = [
		{ key: 'kind', label: 'Kind' },
		{ key: 'status', label: 'Status' },
		{ key: 'file', label: 'File' },
		{ key: 'size', label: 'Size' },
		{ key: 'checksum', label: 'SHA-256' },
		{ key: 'time', label: 'Started / finished' },
		{ key: 'expires', label: 'Expires' },
		{ key: 'restore', label: 'Restore' },
		{ key: 'actions', label: 'Actions', class: 'text-right' }
	];
</script>

<PageHeader
	title="Backups"
	description="Encrypted backups of the platform database, configuration, workspaces and project databases."
>
	{#snippet actions()}
		{#if allowed}
			<Button
				variant="primary"
				onclick={() => {
					kind = 'full';
					createErrors = {};
					createOpen = true;
				}}
				disabled={!cfg?.encryption_configured}><Plus class="size-4" /> Create backup</Button
			>
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else}
	{#if cfg}
		<div class="mb-6 space-y-3">
			{#if !cfg.encryption_configured}
				<Alert tone="danger" title="Backups are impossible: no encryption key configured">
					Set <code class="font-mono">BACKUP_AGE_RECIPIENT</code> to an age public key on the server
					and keep the matching private key offline. See
					<code class="font-mono">docs/backup-restore.md</code>.
				</Alert>
			{/if}
			{#if !cfg.offsite_copy_configured}
				<Alert tone="warning" title="No off-site copy configured">
					Backups are only stored on this server’s disk. A disk failure would destroy the data and
					its only backup. Set <code class="font-mono">BACKUP_COPY_DIR</code> to a mount on another disk
					or machine.
				</Alert>
			{/if}
			<p class="text-sm text-fg-muted">
				Scheduled backup:
				{#if cfg.schedule_enabled}daily at {String(cfg.schedule_hour_utc).padStart(2, '0')}:00 UTC{:else}<strong
						>disabled</strong
					>{/if}
				· retention {cfg.retention_days} days · project databases {cfg.project_databases
					? 'enabled'
					: 'not configured'} · schedule settings are in
				<a href="/admin/settings" class="link">Platform Settings</a>.
			</p>
		</div>
	{/if}

	<Card padded={false}>
		<DataTable
			{columns}
			rows={backups.data?.data ?? []}
			loading={backups.loading && !backups.data}
			error={backups.data ? null : backups.error}
			retry={backups.reload}
		>
			{#snippet empty()}
				<EmptyState
					icon={Archive}
					title="No backups yet"
					description="Run a full backup now and test restoring it regularly."
				>
					{#snippet action()}{#if cfg?.encryption_configured}<Button
								variant="primary"
								onclick={() => (createOpen = true)}><Plus class="size-4" /> Create backup</Button
							>{/if}{/snippet}
				</EmptyState>
			{/snippet}
			{#snippet row(b)}
				<tr class="align-top">
					<td class="px-4 py-3">
						<p class="font-medium">{titleCase(b.kind)}</p>
						<p class="text-xs text-fg-muted">
							{titleCase(b.trigger)}{#if b.triggered_by_username}
								· {b.triggered_by_username}{/if}
						</p>
					</td>
					<td class="px-4 py-3">
						<StatusBadge status={b.status} />
						{#if b.encrypted}<span class="mt-1 block"><Badge tone="success">Encrypted</Badge></span
							>{/if}
						{#if b.error}<p class="mt-1 max-w-xs text-xs break-words text-danger">{b.error}</p>{/if}
					</td>
					<td class="px-4 py-3 font-mono text-xs break-all">{b.file_name ?? '—'}</td>
					<td class="px-4 py-3 whitespace-nowrap tabular-nums">{bytes(b.size_bytes)}</td>
					<td class="px-4 py-3">
						{#if b.checksum_sha256}
							<span
								class="inline-flex items-center gap-1 font-mono text-xs"
								title={b.checksum_sha256}
								>{b.checksum_sha256.slice(0, 12)}…<CopyButton
									text={b.checksum_sha256}
									label="Copy SHA-256 checksum"
								/></span
							>
						{:else}—{/if}
					</td>
					<td class="px-4 py-3 text-xs whitespace-nowrap">
						{dateTime(b.started_at)}<br /><span class="text-fg-muted"
							>{dateTime(b.finished_at)}</span
						>
					</td>
					<td class="px-4 py-3 text-xs whitespace-nowrap" title={dateTime(b.expires_at)}
						>{b.expires_at ? relative(b.expires_at) : '—'}</td
					>
					<td class="px-4 py-3 text-xs">
						{#if b.restore_status}
							<StatusBadge status={b.restore_status} />
							{#if b.restored_at}<span class="mt-1 block text-fg-muted"
									>{relative(b.restored_at)}</span
								>{/if}
							{#if b.restore_error}<p class="mt-1 max-w-xs break-words text-danger">
									{b.restore_error}
								</p>{/if}
						{:else}<span class="text-fg-muted">—</span>{/if}
					</td>
					<td class="px-4 py-3 text-right">
						{#if b.status === 'succeeded' && b.file_name}
							<Button
								size="sm"
								onclick={() => openRestore(b)}
								disabled={b.restore_status === 'queued' || b.restore_status === 'running'}
								><ArchiveRestore class="size-3.5" />
								{verifyOnly(b.kind) ? 'Verify' : 'Restore'}</Button
							>
						{/if}
					</td>
				</tr>
			{/snippet}
		</DataTable>
		{#if backups.data && backups.data.meta.total > perPage}
			<div class="border-t border-border px-4">
				<Pagination bind:page={pageNo} {perPage} total={backups.data.meta.total} />
			</div>
		{/if}
	</Card>
{/if}

<Modal bind:open={createOpen} title="Create backup" size="md">
	<form id="backup-form" onsubmit={create}>
		<fieldset class="space-y-2">
			<legend class="label">What to back up</legend>
			{#each kinds as k (k.id)}
				{@const disabled = k.id === 'project_databases' && !cfg?.project_databases}
				<label
					class="flex items-start gap-2 rounded-md border border-border p-3 text-sm {disabled
						? 'opacity-60'
						: 'hover:bg-panel-2'}"
				>
					<input
						type="radio"
						name="kind"
						class="mt-0.5"
						value={k.id}
						bind:group={kind}
						{disabled}
					/>
					<span>
						<span class="font-medium">{k.label}</span>
						<span class="block text-xs text-fg-muted"
							>{disabled ? 'Project databases are not configured on this server.' : k.help}</span
						>
					</span>
				</label>
			{/each}
			{#if createErrors.kind}<p class="text-xs text-danger" role="alert">
					{createErrors.kind}
				</p>{/if}
		</fieldset>
	</form>
	{#snippet footer()}
		<Button onclick={() => (createOpen = false)}>Cancel</Button>
		<Button type="submit" form="backup-form" variant="primary" loading={creating}
			>Start backup</Button
		>
	{/snippet}
</Modal>

<Modal
	bind:open={restoreOpen}
	title={target ? `${verifyOnly(target.kind) ? 'Verify' : 'Restore'} backup` : 'Restore backup'}
	description={target ? `${titleCase(target.kind)} · ${dateTime(target.finished_at)}` : undefined}
	size="lg"
	onclose={closeRestore}
>
	{#if target}
		<form id="restore-form" class="space-y-4" onsubmit={restore}>
			{#if verifyOnly(target.kind)}
				<Alert tone="info" title="Verification only">
					This backup is decrypted and its integrity checked, but nothing is overwritten. A live
					restore of the platform database, configuration or workspace metadata must be done on the
					server with
					<code class="font-mono">itkctl restore platform-db</code> while the services are stopped —
					see
					<code class="font-mono">docs/backup-restore.md</code>.
				</Alert>
			{:else if target.kind === 'workspace_files'}
				<Alert tone="warning" title="Workspace files are overwritten">
					Restoring replaces files in the affected workspaces with the backed-up versions. Stop the
					workspaces first. Use “Verify” to only check the archive.
				</Alert>
			{:else if target.kind === 'project_databases'}
				<Alert tone="warning" title="The selected database is overwritten">
					Restoring replaces the contents of one project database with its backed-up dump. Connected
					applications should be stopped first. Use “Verify” to only check the archive.
				</Alert>
			{/if}

			{#if !verifyOnly(target.kind)}
				<fieldset>
					<legend class="label">Mode</legend>
					<div class="flex gap-4 text-sm">
						<label class="flex items-center gap-2"
							><input type="radio" value="verify" bind:group={rForm.mode} /> Verify only</label
						>
						<label class="flex items-center gap-2"
							><input type="radio" value="restore" bind:group={rForm.mode} /> Restore</label
						>
					</div>
				</fieldset>
			{/if}

			{#if target.kind === 'workspace_files' && rForm.mode === 'restore'}
				<Field
					label="Workspace ID (optional)"
					id="r-ws"
					error={rErrors.workspace_id}
					hint="Restore only this workspace. Leave empty to restore every workspace in the backup."
				>
					<input
						id="r-ws"
						class="input font-mono"
						bind:value={rForm.workspace_id}
						placeholder="UUID"
					/>
				</Field>
			{/if}
			{#if target.kind === 'project_databases' && rForm.mode === 'restore'}
				<Field label="Target database" id="r-db" required error={rErrors.database_id}>
					<select id="r-db" class="input" bind:value={rForm.database_id} disabled={!databases.data}>
						<option value="">{databases.data ? 'Select a database…' : 'Loading databases…'}</option>
						{#each databases.data?.data ?? [] as d (d.id)}
							<option value={d.id}>{d.project_name} / {d.display_name} ({d.db_name})</option>
						{/each}
					</select>
				</Field>
				{#if databases.error}<p class="text-xs text-danger">
						Databases could not be loaded: {errorMessage(databases.error)}
					</p>{/if}
			{/if}

			<Field
				label="age private key"
				id="r-key"
				required
				error={rErrors.age_identity}
				hint="AGE-SECRET-KEY-… It is used once to decrypt this backup and is never stored."
			>
				<textarea
					id="r-key"
					class="input font-mono text-xs"
					rows="2"
					autocomplete="off"
					spellcheck="false"
					bind:value={rForm.age_identity}></textarea>
			</Field>
			<Field
				label="Reason"
				id="r-reason"
				required
				error={rErrors.reason}
				hint="Recorded in the audit log."
			>
				<input id="r-reason" class="input" bind:value={rForm.reason} />
			</Field>
			<Field label="Type the file name to confirm" id="r-confirm" required error={rErrors.confirm}>
				<p class="mb-1 font-mono text-xs break-all text-fg-muted">{target.file_name}</p>
				<input
					id="r-confirm"
					class="input font-mono"
					autocomplete="off"
					spellcheck="false"
					bind:value={rForm.confirm}
				/>
			</Field>
		</form>
	{/if}
	{#snippet footer()}
		<Button onclick={closeRestore}>Cancel</Button>
		<Button
			type="submit"
			form="restore-form"
			variant={rForm.mode === 'restore' ? 'danger' : 'primary'}
			loading={restoring}
			disabled={!target || rForm.confirm !== target.file_name}
			>{rForm.mode === 'restore' ? 'Restore backup' : 'Verify backup'}</Button
		>
	{/snippet}
</Modal>
