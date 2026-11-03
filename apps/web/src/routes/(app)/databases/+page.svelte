<script lang="ts">
	import { onMount } from 'svelte';
	import { Database as DatabaseIcon, Plus } from '@lucide/svelte';
	import { page } from '$app/state';
	import {
		Alert,
		Button,
		Card,
		DataTable,
		EmptyState,
		Field,
		Modal,
		PageHeader,
		Spinner,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import CredentialsModal from '#lib/components/databases/CredentialsModal.svelte';
	import { api, ApiError, errorMessage, idempotencyKey } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Database, DatabaseCredentials, Project } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes, date } from '#lib/format.ts';

	const list = resource(() => api.get<{ databases: Database[]; enabled: boolean }>('/databases'));
	const rows = $derived(list.data?.databases ?? []);
	const enabled = $derived(list.data?.enabled ?? false);
	const canCreate = $derived(auth.can('databases.create', 'databases.manage_all'));

	// Poll while any database is changing state.
	const pending = $derived(
		rows.some((d) => d.status === 'provisioning' || d.status === 'deleting')
	);
	$effect(() => {
		if (!pending) return;
		const t = setInterval(() => {
			if (document.visibilityState === 'visible') list.reload();
		}, 3000);
		return () => clearInterval(t);
	});

	// ---- Create
	let createOpen = $state(false);
	let projects = $state<Project[] | null>(null);
	let projectsError = $state('');
	let projectId = $state('');
	let displayName = $state('');
	let connectionLimit = $state(20);
	let key = '';
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let formError = $state('');
	let credentials = $state<DatabaseCredentials | null>(null);

	async function loadProjects(): Promise<Project[]> {
		projectsError = '';
		try {
			const r = await api.list<Project>('/projects', {
				status: 'active',
				per_page: 100,
				sort: 'name'
			});
			const manageAll = auth.can('projects.manage_all');
			const eligible = r.data.filter(
				(p) => manageAll || p.my_role === 'owner' || p.my_role === 'maintainer'
			);
			projects = eligible;
			return eligible;
		} catch (err) {
			projects = [];
			projectsError = errorMessage(err);
			return [];
		}
	}

	async function openCreate(preselect = '') {
		displayName = '';
		connectionLimit = 20;
		errors = {};
		formError = '';
		key = idempotencyKey();
		createOpen = true;
		projects = null;
		const eligible = await loadProjects();
		projectId =
			preselect && eligible.some((p) => p.id === preselect) ? preselect : (eligible[0]?.id ?? '');
	}

	onMount(() => {
		if (page.url.searchParams.get('new') === '1' && canCreate) {
			// Wait for `enabled` before opening the dialog.
			list.reload().then(() => {
				if (list.data?.enabled) openCreate(page.url.searchParams.get('project') ?? '');
			});
		}
	});

	async function create(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		errors = {};
		formError = '';
		try {
			const r = await api.post<{ database: Database; credentials: DatabaseCredentials }>(
				'/databases',
				{
					project_id: projectId,
					display_name: displayName.trim(),
					connection_limit: Number(connectionLimit)
				},
				{ headers: { 'Idempotency-Key': key } }
			);
			createOpen = false;
			credentials = r.credentials;
			toast.success('Database is being provisioned', r.database.display_name);
			list.reload();
		} catch (err) {
			if (err instanceof ApiError && err.status === 422 && Object.keys(err.details).length) {
				errors = err.details;
			} else {
				formError = errorMessage(err);
				// A different key lets the user retry after fixing the cause.
				if (err instanceof ApiError && err.status >= 400 && err.status < 500)
					key = idempotencyKey();
			}
		} finally {
			saving = false;
		}
	}
</script>

<PageHeader
	title="Databases"
	description="PostgreSQL databases provisioned for your projects. Each database has its own login role."
>
	{#snippet actions()}
		{#if canCreate && enabled}
			<Button variant="primary" onclick={() => openCreate()}
				><Plus class="size-4" /> New database</Button
			>
		{/if}
	{/snippet}
</PageHeader>

{#if list.data && !enabled}
	<div class="mb-4">
		<Alert tone="info" title="Project databases are not configured on this server">
			The administrator has not connected a PostgreSQL server for project databases, so new
			databases cannot be created.{#if rows.length > 0}
				Existing databases are listed below for reference.{/if}
		</Alert>
	</div>
{/if}

<Card padded={false}>
	<DataTable
		columns={[
			{ key: 'name', label: 'Database' },
			{ key: 'project', label: 'Project' },
			{ key: 'status', label: 'Status' },
			{ key: 'size', label: 'Size', class: 'text-right' },
			{ key: 'limit', label: 'Conn. limit', class: 'text-right' },
			{ key: 'created_by', label: 'Created by' },
			{ key: 'created', label: 'Created' }
		]}
		{rows}
		loading={list.loading}
		error={list.error}
		retry={list.reload}
	>
		{#snippet row(d: Database)}
			<tr class="hover:bg-panel-2/50">
				<td class="px-4 py-3">
					<a href="/databases/{d.id}" class="font-medium text-fg hover:underline"
						>{d.display_name}</a
					>
					<p class="font-mono text-xs text-fg-muted">{d.db_name}</p>
				</td>
				<td class="px-4 py-3">
					<a href="/projects/{d.project_id}" class="link">{d.project_name}</a>
				</td>
				<td class="px-4 py-3">
					<span class="inline-flex items-center gap-2">
						<StatusBadge status={d.status} />
						{#if d.status === 'provisioning' || d.status === 'deleting'}<Spinner size={12} />{/if}
					</span>
				</td>
				<td class="px-4 py-3 text-right tabular-nums">{bytes(d.size_bytes)}</td>
				<td class="px-4 py-3 text-right tabular-nums">{d.connection_limit}</td>
				<td class="px-4 py-3 text-fg-muted">{d.created_by_username}</td>
				<td class="px-4 py-3 whitespace-nowrap text-fg-muted">{date(d.created_at)}</td>
			</tr>
		{/snippet}
		{#snippet empty()}
			<EmptyState
				icon={DatabaseIcon}
				title="No databases"
				description={!enabled
					? 'Project databases are unavailable on this server.'
					: canCreate
						? 'Create a PostgreSQL database for one of your projects. You need to be an owner or maintainer of the project.'
						: 'Databases of projects you belong to will appear here.'}
			>
				{#snippet action()}
					{#if canCreate && enabled}
						<Button variant="primary" onclick={() => openCreate()}
							><Plus class="size-4" /> New database</Button
						>
					{/if}
				{/snippet}
			</EmptyState>
		{/snippet}
	</DataTable>
</Card>

<Modal
	bind:open={createOpen}
	title="New database"
	description="A PostgreSQL database and a dedicated login role are created for the project."
>
	{#if projects === null}
		<div class="flex justify-center py-6"><Spinner /></div>
	{:else if projectsError}
		<Alert tone="danger" title="Could not load your projects">{projectsError}</Alert>
	{:else if projects.length === 0}
		<EmptyState
			title="No eligible project"
			description="You must be an owner or maintainer of an active project to create a database."
		>
			{#snippet action()}
				{#if auth.can('projects.create')}<Button href="/projects?new=1">Create a project</Button
					>{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<form id="create-database" class="space-y-4" onsubmit={create}>
			{#if formError}<Alert tone="danger">{formError}</Alert>{/if}
			<Field label="Project" id="db-project" error={errors.project_id} required>
				<select id="db-project" class="input" bind:value={projectId} required>
					{#each projects as p (p.id)}
						<option value={p.id}>{p.name} ({p.slug})</option>
					{/each}
				</select>
			</Field>
			<Field
				label="Display name"
				id="db-name"
				error={errors.display_name}
				hint="Used to derive the PostgreSQL database name."
				required
			>
				<input
					id="db-name"
					class="input"
					bind:value={displayName}
					maxlength="80"
					required
					placeholder="e.g. Research data"
				/>
			</Field>
			<Field
				label="Connection limit"
				id="db-limit"
				error={errors.connection_limit}
				hint="Maximum simultaneous connections for this database's role (1–200)."
			>
				<input
					id="db-limit"
					type="number"
					class="input w-32"
					min="1"
					max="200"
					bind:value={connectionLimit}
				/>
			</Field>
			<p class="text-xs text-fg-muted">
				The password is generated by the server and shown only once after creation.
			</p>
		</form>
	{/if}
	{#snippet footer()}
		<Button onclick={() => (createOpen = false)}>Cancel</Button>
		<Button
			type="submit"
			form="create-database"
			variant="primary"
			loading={saving}
			disabled={!projects?.length || !projectId || !displayName.trim()}>Create database</Button
		>
	{/snippet}
</Modal>

<CredentialsModal bind:credentials title="Database created — save the credentials" />
