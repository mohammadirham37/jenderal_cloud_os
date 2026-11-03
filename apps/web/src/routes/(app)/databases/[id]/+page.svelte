<script lang="ts">
	import { KeyRound, Trash2 } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		Alert,
		Button,
		Card,
		CopyButton,
		ErrorState,
		PageHeader,
		Skeleton,
		Spinner,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import CredentialsModal from '#lib/components/databases/CredentialsModal.svelte';
	import { api, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Database, DatabaseCredentials } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { crumbs } from '#lib/stores/breadcrumb.svelte.ts';
	import { bytes, dateTime, relative, titleCase } from '#lib/format.ts';

	const id = $derived(page.params.id ?? '');
	const db = resource(() => api.get<Database>(`/databases/${id}`));
	const d = $derived(db.data);

	// The project role decides whether credentials can be reset (maintainer+).
	const projectId = $derived(db.data?.project_id);
	const project = resource(async () => {
		const pid = projectId;
		if (!pid) return null;
		try {
			return await api.get<{ can_manage: boolean }>(`/projects/${pid}`);
		} catch {
			return null;
		}
	});

	const pending = $derived(d?.status === 'provisioning' || d?.status === 'deleting');
	$effect(() => {
		if (!pending) return;
		const t = setInterval(() => {
			if (document.visibilityState === 'visible') db.reload();
		}, 3000);
		return () => clearInterval(t);
	});

	$effect(() => {
		if (d) crumbs.set(d.id, d.display_name);
	});

	const canManage = $derived(auth.can('databases.manage_all') || !!project.data?.can_manage);
	const canDelete = $derived(canManage && auth.can('databases.delete', 'databases.manage_all'));

	let busy = $state(false);
	let credentials = $state<DatabaseCredentials | null>(null);

	async function resetCredentials() {
		if (!d) return;
		const ok = await confirmDialog.confirm({
			title: 'Reset credentials?',
			message: `A new password is generated for role ${d.role_name}. The current password stops working immediately, so applications using it must be updated.`,
			confirmLabel: 'Reset password',
			danger: true
		});
		if (!ok) return;
		busy = true;
		try {
			credentials = await api.post<DatabaseCredentials>(`/databases/${d.id}/credentials/reset`);
			toast.success('Credentials reset', 'Save the new password now.');
		} catch (err) {
			toast.error('Could not reset credentials', errorMessage(err));
		} finally {
			busy = false;
		}
	}

	async function remove() {
		if (!d) return;
		const { confirmed } = await confirmDialog.ask({
			title: `Delete ${d.display_name}?`,
			message:
				'The database and its login role are dropped and active connections are terminated. All data in it is lost permanently.',
			confirmLabel: 'Delete database',
			danger: true,
			requireText: d.db_name
		});
		if (!confirmed) return;
		busy = true;
		try {
			await api.del(`/databases/${d.id}`, { confirm: d.db_name });
			toast.success('Database deletion started', d.display_name);
			await goto('/databases');
		} catch (err) {
			toast.error('Could not delete the database', errorMessage(err));
		} finally {
			busy = false;
		}
	}

	const psql = $derived(
		d
			? `psql "host=${d.host} port=${d.port} dbname=${d.db_name} user=${d.role_name} sslmode=prefer"`
			: ''
	);
	const url = $derived(
		d
			? `postgresql://${d.role_name}:<password>@${d.host}:${d.port}/${d.db_name}?sslmode=prefer`
			: ''
	);
	const env = $derived(d ? `DATABASE_URL="${url}"` : '');
</script>

{#if db.error}
	<PageHeader title="Database" />
	<Card><ErrorState error={db.error} retry={db.reload} /></Card>
{:else if !d}
	<PageHeader title="Database" />
	<Card><Skeleton lines={6} /></Card>
{:else}
	<PageHeader title={d.display_name} description="PostgreSQL database in project {d.project_name}">
		{#snippet meta()}
			<StatusBadge status={d.status} />
			{#if pending}<Spinner size={14} />{/if}
			<span class="font-mono text-xs">{d.db_name}</span>
		{/snippet}
		{#snippet actions()}
			{#if canManage}
				<Button onclick={resetCredentials} disabled={busy || d.status !== 'ready'}
					><KeyRound class="size-4" /> Reset credentials</Button
				>
			{/if}
			{#if canDelete}
				<Button
					variant="danger"
					onclick={remove}
					disabled={busy || (d.status !== 'ready' && d.status !== 'failed')}
					><Trash2 class="size-4" /> Delete</Button
				>
			{/if}
		{/snippet}
	</PageHeader>

	{#if d.status === 'provisioning'}
		<div class="mb-4">
			<Alert tone="info" title="Provisioning">
				The database is being created. This page updates automatically.
			</Alert>
		</div>
	{:else if d.status === 'deleting'}
		<div class="mb-4">
			<Alert tone="warning" title="Deleting">The database is being deleted.</Alert>
		</div>
	{:else if d.status === 'failed'}
		<div class="mb-4">
			<Alert tone="danger" title="Provisioning failed">
				{d.status_reason ?? 'No reason was recorded.'} You can delete this database and try again.
			</Alert>
		</div>
	{/if}

	<div class="grid gap-6 lg:grid-cols-3">
		<Card
			title="Connection"
			description="The password is never shown here. Reset the credentials if it was lost."
			class="lg:col-span-2"
		>
			<dl class="divide-y divide-border rounded-md border border-border">
				{#each [{ label: 'Host', value: d.host }, { label: 'Port', value: String(d.port) }, { label: 'Database', value: d.db_name }, { label: 'Username', value: d.role_name }] as r (r.label)}
					<div class="flex items-center gap-3 px-3 py-2">
						<dt class="w-28 shrink-0 text-sm text-fg-muted">{r.label}</dt>
						<dd class="min-w-0 flex-1 font-mono text-sm break-all">{r.value}</dd>
						<CopyButton text={r.value} label="Copy {r.label.toLowerCase()}" />
					</div>
				{/each}
				<div class="flex items-center gap-3 px-3 py-2">
					<dt class="w-28 shrink-0 text-sm text-fg-muted">Password</dt>
					<dd class="min-w-0 flex-1 text-sm text-fg-muted">Shown only when created or reset</dd>
				</div>
			</dl>

			<h3 class="mt-6 text-sm font-semibold">Connect</h3>
			<div class="mt-2 space-y-3">
				{#each [{ label: 'psql', value: psql }, { label: 'Connection string', value: url }, { label: 'Environment variable', value: env }] as s (s.label)}
					<div>
						<p class="mb-1 text-xs text-fg-muted">{s.label}</p>
						<div
							class="flex items-start gap-2 rounded-md border border-border bg-panel-2 px-3 py-2"
						>
							<code class="min-w-0 flex-1 font-mono text-xs break-all">{s.value}</code>
							<CopyButton text={s.value} label="Copy {s.label}" />
						</div>
					</div>
				{/each}
				<p class="text-xs text-fg-muted">
					Replace <span class="kbd">&lt;password&gt;</span> with the password you saved. psql prompts
					for it.
				</p>
			</div>
		</Card>

		<Card title="Details">
			<dl class="space-y-2 text-sm">
				<div class="flex justify-between gap-2">
					<dt class="text-fg-muted">Project</dt>
					<dd><a href="/projects/{d.project_id}" class="link">{d.project_name}</a></dd>
				</div>
				<div class="flex justify-between gap-2">
					<dt class="text-fg-muted">Engine</dt>
					<dd>{titleCase(d.engine)}</dd>
				</div>
				<div class="flex justify-between gap-2">
					<dt class="text-fg-muted">Size</dt>
					<dd class="tabular-nums" title={d.size_bytes === null ? 'Not measured yet' : undefined}>
						{d.size_bytes === null ? 'Not measured yet' : bytes(d.size_bytes)}
					</dd>
				</div>
				<div class="flex justify-between gap-2">
					<dt class="text-fg-muted">Connection limit</dt>
					<dd class="tabular-nums">{d.connection_limit}</dd>
				</div>
				<div class="flex justify-between gap-2">
					<dt class="text-fg-muted">Last backup</dt>
					<dd class="text-right">
						{#if d.last_backup_at}
							<span title={dateTime(d.last_backup_at)}>{relative(d.last_backup_at)}</span>
							{#if d.last_backup_status}<br /><StatusBadge status={d.last_backup_status} />{/if}
						{:else}
							<span class="text-fg-muted">No backup yet</span>
						{/if}
					</dd>
				</div>
				<div class="flex justify-between gap-2">
					<dt class="text-fg-muted">Created by</dt>
					<dd>{d.created_by_username}</dd>
				</div>
				<div class="flex justify-between gap-2">
					<dt class="text-fg-muted">Created</dt>
					<dd>{dateTime(d.created_at)}</dd>
				</div>
			</dl>
		</Card>
	</div>
{/if}

<CredentialsModal bind:credentials title="New credentials — save the password" />
