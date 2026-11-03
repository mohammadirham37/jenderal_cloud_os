<script lang="ts">
	import {
		Archive,
		ArchiveRestore,
		Database as DatabaseIcon,
		History,
		Layers,
		LogOut,
		Pencil,
		Plus,
		Trash2,
		UserPlus,
		Users
	} from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		Alert,
		Badge,
		Button,
		Card,
		DataTable,
		EmptyState,
		ErrorState,
		Field,
		Meter,
		Modal,
		PageHeader,
		Skeleton,
		StatCard,
		StatusBadge,
		Tabs
	} from '#lib/components/ui/index.ts';
	import UserPicker from '#lib/components/projects/UserPicker.svelte';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type {
		Database,
		Project,
		ProjectMember,
		Quota,
		Usage,
		Workspace
	} from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { crumbs } from '#lib/stores/breadcrumb.svelte.ts';
	import { bytes, cores, date, dateTime, relative, titleCase } from '#lib/format.ts';

	interface ProjectDetail {
		project: Project;
		members: ProjectMember[];
		usage: Usage;
		quota: Quota | null;
		can_manage: boolean;
		is_owner: boolean;
	}
	interface ActivityItem {
		id: number;
		occurred_at: string;
		actor_username: string | null;
		action: string;
		resource_type: string;
		resource_id: string;
		outcome: string;
	}
	type MemberRole = 'maintainer' | 'member' | 'viewer';

	const id = $derived(page.params.id ?? '');

	const detail = resource(() => api.get<ProjectDetail>(`/projects/${id}`));
	const workspaces = resource(() =>
		api.list<Workspace>('/workspaces', { project_id: id, per_page: 100 })
	);
	const databases = resource(() =>
		api.get<{ databases: Database[]; enabled: boolean }>('/databases', { project_id: id })
	);
	const activity = resource(() => api.get<ActivityItem[]>(`/projects/${id}/activity`));

	const d = $derived(detail.data);
	const p = $derived(d?.project);
	const meId = $derived(auth.me?.id ?? '');
	const myMembership = $derived(d?.members.find((m) => m.user_id === meId));
	const archived = $derived(p?.status === 'archived');

	$effect(() => {
		if (p) crumbs.set(p.id, p.name);
	});

	let tab = $state('overview');
	const tabs = $derived([
		{ id: 'overview', label: 'Overview' },
		{ id: 'members', label: 'Members', count: d?.members.length },
		{ id: 'workspaces', label: 'Workspaces', count: p?.workspace_count },
		{ id: 'databases', label: 'Databases', count: p?.database_count },
		{ id: 'activity', label: 'Activity' }
	]);

	// ---- Edit name / description
	let editOpen = $state(false);
	let editName = $state('');
	let editDescription = $state('');
	let editErrors = $state<Record<string, string>>({});
	let editError = $state('');
	let saving = $state(false);

	function openEdit() {
		if (!p) return;
		editName = p.name;
		editDescription = p.description;
		editErrors = {};
		editError = '';
		editOpen = true;
	}

	async function saveEdit(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		editErrors = {};
		editError = '';
		try {
			const r = await api.patch<ProjectDetail>(`/projects/${id}`, {
				name: editName.trim(),
				description: editDescription.trim()
			});
			detail.set(r);
			editOpen = false;
			toast.success('Project updated');
		} catch (err) {
			if (err instanceof ApiError && err.status === 422 && Object.keys(err.details).length) {
				editErrors = err.details;
			} else {
				editError = errorMessage(err);
			}
		} finally {
			saving = false;
		}
	}

	// ---- Archive / delete
	let busy = $state(false);

	async function setArchived(value: boolean) {
		if (!p) return;
		const ok = await confirmDialog.confirm({
			title: value ? `Archive ${p.name}?` : `Unarchive ${p.name}?`,
			message: value
				? 'Archived projects stay readable, but new databases cannot be created in them. You can unarchive the project later.'
				: 'The project becomes active again.',
			confirmLabel: value ? 'Archive project' : 'Unarchive project',
			danger: value
		});
		if (!ok) return;
		busy = true;
		try {
			const r = await api.patch<ProjectDetail>(`/projects/${id}`, {
				status: value ? 'archived' : 'active'
			});
			detail.set(r);
			toast.success(value ? 'Project archived' : 'Project unarchived');
		} catch (err) {
			toast.error('Could not update the project', errorMessage(err));
		} finally {
			busy = false;
		}
	}

	async function deleteProject() {
		if (!p) return;
		const { confirmed } = await confirmDialog.ask({
			title: `Delete ${p.name}?`,
			message:
				'The project, its member list and quota assignment are deleted permanently. This cannot be undone.',
			confirmLabel: 'Delete project',
			danger: true,
			requireText: p.slug
		});
		if (!confirmed) return;
		busy = true;
		try {
			await api.del(`/projects/${id}`);
			toast.success('Project deleted', p.name);
			await goto('/projects');
		} catch (err) {
			toast.error('Could not delete the project', errorMessage(err));
		} finally {
			busy = false;
		}
	}

	// ---- Members
	let addUsername = $state('');
	let addRole = $state<MemberRole>('member');
	let adding = $state(false);
	let addError = $state('');
	let roleBusy = $state<string | null>(null);

	async function addMember(e: SubmitEvent) {
		e.preventDefault();
		if (!addUsername.trim()) return;
		adding = true;
		addError = '';
		try {
			const members = await api.post<ProjectMember[]>(`/projects/${id}/members`, {
				username: addUsername.trim(),
				role: addRole
			});
			if (d) detail.set({ ...d, members });
			toast.success('Member added', `${addUsername.trim()} is now ${addRole}.`);
			addUsername = '';
			detail.reload();
		} catch (err) {
			addError =
				err instanceof ApiError && Object.keys(err.details).length
					? Object.values(err.details).join(' ')
					: errorMessage(err);
		} finally {
			adding = false;
		}
	}

	async function changeRole(m: ProjectMember, role: MemberRole) {
		if (role === m.role) return;
		roleBusy = m.user_id;
		try {
			const members = await api.post<ProjectMember[]>(`/projects/${id}/members`, {
				user_id: m.user_id,
				role
			});
			if (d) detail.set({ ...d, members });
			toast.success('Role changed', `${m.username} is now ${role}.`);
		} catch (err) {
			toast.error('Could not change the role', errorMessage(err));
			detail.reload();
		} finally {
			roleBusy = null;
		}
	}

	async function removeMember(m: ProjectMember) {
		const ok = await confirmDialog.confirm({
			title: `Remove ${m.username}?`,
			message: `${m.full_name || m.username} loses access to this project's workspaces and databases.`,
			confirmLabel: 'Remove member',
			danger: true
		});
		if (!ok) return;
		roleBusy = m.user_id;
		try {
			await api.del(`/projects/${id}/members/${m.user_id}`);
			toast.success('Member removed', m.username);
			await detail.reload();
		} catch (err) {
			toast.error('Could not remove the member', errorMessage(err));
		} finally {
			roleBusy = null;
		}
	}

	async function leave() {
		if (!p) return;
		const ok = await confirmDialog.confirm({
			title: `Leave ${p.name}?`,
			message:
				'You will lose access to the project. A maintainer or the owner has to add you again to regain access.',
			confirmLabel: 'Leave project',
			danger: true
		});
		if (!ok) return;
		busy = true;
		try {
			await api.del(`/projects/${id}/members/${meId}`);
			toast.success('You left the project', p.name);
			await goto('/projects');
		} catch (err) {
			toast.error('Could not leave the project', errorMessage(err));
		} finally {
			busy = false;
		}
	}

	function canEditMember(m: ProjectMember): boolean {
		if (!d?.can_manage || m.role === 'owner' || m.user_id === meId) return false;
		if (m.role === 'maintainer' && !d.is_owner) return false;
		return true;
	}

	const roleTone = {
		owner: 'accent',
		maintainer: 'info',
		member: 'neutral',
		viewer: 'neutral'
	} as const;
	const canCreateWorkspace = $derived(
		auth.can('workspaces.create') &&
			!archived &&
			(d?.can_manage || p?.my_role === 'member' || p?.my_role === 'owner')
	);
	const canCreateDatabase = $derived(
		auth.can('databases.create', 'databases.manage_all') &&
			!!d?.can_manage &&
			!archived &&
			databases.data?.enabled !== false
	);
</script>

{#if detail.error}
	<PageHeader title="Project" />
	<Card><ErrorState error={detail.error} retry={detail.reload} /></Card>
{:else if !d || !p}
	<PageHeader title="Project" />
	<Card><Skeleton lines={6} /></Card>
{:else}
	<PageHeader title={p.name} description={p.description || undefined}>
		{#snippet meta()}
			<span class="font-mono text-xs">{p.slug}</span>
			<StatusBadge status={p.status} />
			{#if p.my_role}<Badge tone={roleTone[p.my_role]}>{titleCase(p.my_role)}</Badge>{/if}
			<span>Owner: {p.owner_username}</span>
		{/snippet}
		{#snippet actions()}
			{#if d.can_manage}
				<Button onclick={openEdit}><Pencil class="size-4" /> Edit</Button>
			{/if}
			{#if myMembership && myMembership.role !== 'owner'}
				<Button onclick={leave} disabled={busy}><LogOut class="size-4" /> Leave project</Button>
			{/if}
		{/snippet}
	</PageHeader>

	{#if archived}
		<div class="mb-4">
			<Alert tone="warning" title="This project is archived">
				Its resources remain accessible, but new databases cannot be created.
				{#if d.is_owner}Unarchive it from the Overview tab to make it active again.{/if}
			</Alert>
		</div>
	{/if}

	<Tabs {tabs} bind:active={tab} />

	<div class="mt-6" role="tabpanel" aria-labelledby="tab-{tab}">
		{#if tab === 'overview'}
			<div class="grid gap-4 sm:grid-cols-3">
				<StatCard label="Members" value={String(d.members.length)} icon={Users} />
				<StatCard label="Workspaces" value={String(p.workspace_count)} icon={Layers} />
				<StatCard label="Databases" value={String(p.database_count)} icon={DatabaseIcon} />
			</div>

			<div class="mt-6 grid gap-6 lg:grid-cols-3">
				<Card title="About" class="lg:col-span-2">
					{#if p.description}
						<p class="text-sm whitespace-pre-line text-fg">{p.description}</p>
					{:else}
						<p class="text-sm text-fg-muted">
							No description.{#if d.can_manage}
								Use <em>Edit</em> to describe the project's purpose.{/if}
						</p>
					{/if}
					<dl class="mt-4 grid gap-x-6 gap-y-2 border-t border-border pt-4 text-sm sm:grid-cols-2">
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Slug</dt>
							<dd class="font-mono">{p.slug}</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Owner</dt>
							<dd>{p.owner_username}</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Created</dt>
							<dd>{dateTime(p.created_at)}</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Last updated</dt>
							<dd>{relative(p.updated_at)}</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Quota package</dt>
							<dd>{p.quota_key ?? 'None (members’ personal quotas apply)'}</dd>
						</div>
					</dl>
				</Card>

				<Card title="Project quota">
					{#if d.quota}
						<p class="mb-4 text-sm text-fg-muted">
							{d.quota.name} — limits shared by all workspaces and databases in this project.
						</p>
						<div class="space-y-3">
							<Meter
								label="Workspaces"
								value={d.usage.workspaces}
								max={d.quota.max_workspaces}
								display="{d.usage.workspaces} / {d.quota.max_workspaces}"
							/>
							<Meter
								label="Databases"
								value={d.usage.databases}
								max={d.quota.max_databases}
								display="{d.usage.databases} / {d.quota.max_databases}"
							/>
							<Meter
								label="CPU (running)"
								value={d.usage.running_cpu_millis}
								max={d.quota.cpu_millis}
								display="{cores(d.usage.running_cpu_millis)} / {cores(d.quota.cpu_millis)}"
							/>
							<Meter
								label="Memory (running)"
								value={d.usage.running_memory_bytes}
								max={d.quota.memory_bytes}
								display="{bytes(d.usage.running_memory_bytes)} / {bytes(d.quota.memory_bytes)}"
							/>
							<Meter
								label="Storage allocated"
								value={d.usage.allocated_storage_bytes}
								max={d.quota.storage_bytes}
								display="{bytes(d.usage.allocated_storage_bytes)} / {bytes(d.quota.storage_bytes)}"
							/>
						</div>
					{:else}
						<p class="text-sm text-fg-muted">
							No project quota is assigned. Resources count against each member's personal quota. An
							administrator can assign a project quota package.
						</p>
					{/if}
				</Card>
			</div>

			{#if d.is_owner}
				<Card title="Danger zone" class="mt-6">
					<div class="space-y-4">
						<div class="flex flex-wrap items-center justify-between gap-3">
							<div>
								<p class="text-sm font-medium">
									{archived ? 'Unarchive project' : 'Archive project'}
								</p>
								<p class="text-sm text-fg-muted">
									{archived
										? 'Make the project active again.'
										: 'Mark the project as finished. It stays readable.'}
								</p>
							</div>
							<Button onclick={() => setArchived(!archived)} disabled={busy}>
								{#if archived}<ArchiveRestore class="size-4" /> Unarchive{:else}<Archive
										class="size-4"
									/> Archive{/if}
							</Button>
						</div>
						<div
							class="flex flex-wrap items-center justify-between gap-3 border-t border-border pt-4"
						>
							<div>
								<p class="text-sm font-medium">Delete project</p>
								<p class="text-sm text-fg-muted">
									{#if p.workspace_count > 0 || p.database_count > 0}
										Delete or move its {p.workspace_count} workspace(s) and {p.database_count}
										database(s) first.
									{:else}
										Permanently delete this project.
									{/if}
								</p>
							</div>
							<Button
								variant="danger"
								onclick={deleteProject}
								disabled={busy || p.workspace_count > 0 || p.database_count > 0}
								><Trash2 class="size-4" /> Delete project</Button
							>
						</div>
					</div>
				</Card>
			{/if}
		{:else if tab === 'members'}
			{#if d.can_manage}
				<Card title="Add member" class="mb-6">
					<form class="grid gap-3 sm:grid-cols-[1fr_12rem_auto] sm:items-end" onsubmit={addMember}>
						<Field label="User" id="add-member-user" error={addError || undefined}>
							<UserPicker
								id="add-member-user"
								bind:value={addUsername}
								invalid={!!addError}
								exclude={d.members.map((m) => m.user_id)}
							/>
						</Field>
						<Field label="Role" id="add-member-role">
							<select id="add-member-role" class="input" bind:value={addRole}>
								{#if d.is_owner}<option value="maintainer">Maintainer</option>{/if}
								<option value="member">Member</option>
								<option value="viewer">Viewer</option>
							</select>
						</Field>
						<Button type="submit" variant="primary" loading={adding} disabled={!addUsername.trim()}
							><UserPlus class="size-4" /> Add</Button
						>
					</form>
					<p class="mt-3 text-xs text-fg-muted">
						Maintainers manage members and databases · Members create workspaces in the project ·
						Viewers have read-only access.
					</p>
				</Card>
			{/if}
			<Card padded={false}>
				<DataTable
					columns={[
						{ key: 'user', label: 'User' },
						{ key: 'role', label: 'Role' },
						{ key: 'added', label: 'Added' },
						{ key: 'actions', label: 'Actions', class: 'text-right' }
					]}
					rows={d.members}
					emptyTitle="No members"
				>
					{#snippet row(m: ProjectMember)}
						<tr>
							<td class="px-4 py-3">
								<p class="font-medium">
									{m.full_name || m.username}{#if m.user_id === meId}
										<span class="text-xs text-fg-muted">(you)</span>{/if}
								</p>
								<p class="text-xs text-fg-muted">{m.username}</p>
							</td>
							<td class="px-4 py-3">
								{#if canEditMember(m)}
									<label class="sr-only" for="role-{m.user_id}">Role of {m.username}</label>
									<select
										id="role-{m.user_id}"
										class="input w-auto py-1"
										value={m.role}
										disabled={roleBusy === m.user_id}
										onchange={(e) => changeRole(m, e.currentTarget.value as MemberRole)}
									>
										{#if d.is_owner || m.role === 'maintainer'}<option value="maintainer"
												>Maintainer</option
											>{/if}
										<option value="member">Member</option>
										<option value="viewer">Viewer</option>
									</select>
								{:else}
									<Badge tone={roleTone[m.role]}>{titleCase(m.role)}</Badge>
								{/if}
							</td>
							<td class="px-4 py-3 text-fg-muted">{date(m.created_at)}</td>
							<td class="px-4 py-3 text-right">
								{#if m.role === 'owner'}
									<span class="text-xs text-fg-subtle">Owner cannot be removed</span>
								{:else if m.user_id === meId}
									<Button size="sm" onclick={leave} disabled={busy}>Leave</Button>
								{:else if canEditMember(m)}
									<Button
										size="sm"
										variant="ghost"
										onclick={() => removeMember(m)}
										disabled={roleBusy === m.user_id}
										aria-label="Remove {m.username}"><Trash2 class="size-3.5" /> Remove</Button
									>
								{/if}
							</td>
						</tr>
					{/snippet}
				</DataTable>
			</Card>
		{:else if tab === 'workspaces'}
			<Card title="Workspaces" padded={false}>
				{#snippet actions()}
					{#if canCreateWorkspace}
						<Button size="sm" variant="primary" href="/workspaces/new?project_id={p.id}"
							><Plus class="size-3.5" /> New workspace in this project</Button
						>
					{/if}
				{/snippet}
				<DataTable
					columns={[
						{ key: 'name', label: 'Workspace' },
						{ key: 'owner', label: 'Owner' },
						{ key: 'template', label: 'Template' },
						{ key: 'status', label: 'Status' },
						{ key: 'created', label: 'Created' }
					]}
					rows={workspaces.data?.data ?? []}
					loading={workspaces.loading}
					error={workspaces.error}
					retry={workspaces.reload}
				>
					{#snippet row(w: Workspace)}
						<tr class="hover:bg-panel-2/50">
							<td class="px-4 py-3">
								<a href="/workspaces/{w.id}" class="font-medium hover:underline">{w.name}</a>
								<p class="font-mono text-xs text-fg-muted">{w.slug}</p>
							</td>
							<td class="px-4 py-3 text-fg-muted">{w.owner_username}</td>
							<td class="px-4 py-3 text-fg-muted">{w.template_name}</td>
							<td class="px-4 py-3"><StatusBadge status={w.status} pending={w.pending_job} /></td>
							<td class="px-4 py-3 text-fg-muted">{date(w.created_at)}</td>
						</tr>
					{/snippet}
					{#snippet empty()}
						<EmptyState
							icon={Layers}
							title="No workspaces in this project"
							description={canCreateWorkspace
								? 'Create a workspace and attach it to this project so members can collaborate.'
								: 'Workspaces attached to this project will appear here.'}
						>
							{#snippet action()}
								{#if canCreateWorkspace}
									<Button variant="primary" href="/workspaces/new?project_id={p.id}"
										><Plus class="size-4" /> New workspace</Button
									>
								{/if}
							{/snippet}
						</EmptyState>
					{/snippet}
				</DataTable>
			</Card>
		{:else if tab === 'databases'}
			{#if databases.data && !databases.data.enabled}
				<div class="mb-4">
					<Alert tone="info" title="Project databases are not configured on this server">
						An administrator must configure a PostgreSQL server for project databases before they
						can be created.
					</Alert>
				</div>
			{/if}
			<Card title="Databases" padded={false}>
				{#snippet actions()}
					{#if canCreateDatabase}
						<Button size="sm" variant="primary" href="/databases?new=1&project={p.id}"
							><Plus class="size-3.5" /> New database</Button
						>
					{/if}
				{/snippet}
				<DataTable
					columns={[
						{ key: 'name', label: 'Database' },
						{ key: 'status', label: 'Status' },
						{ key: 'size', label: 'Size', class: 'text-right' },
						{ key: 'created_by', label: 'Created by' },
						{ key: 'created', label: 'Created' }
					]}
					rows={databases.data?.databases ?? []}
					loading={databases.loading}
					error={databases.error}
					retry={databases.reload}
				>
					{#snippet row(db: Database)}
						<tr class="hover:bg-panel-2/50">
							<td class="px-4 py-3">
								<a href="/databases/{db.id}" class="font-medium hover:underline"
									>{db.display_name}</a
								>
								<p class="font-mono text-xs text-fg-muted">{db.db_name}</p>
							</td>
							<td class="px-4 py-3"><StatusBadge status={db.status} /></td>
							<td class="px-4 py-3 text-right tabular-nums">{bytes(db.size_bytes)}</td>
							<td class="px-4 py-3 text-fg-muted">{db.created_by_username}</td>
							<td class="px-4 py-3 text-fg-muted">{date(db.created_at)}</td>
						</tr>
					{/snippet}
					{#snippet empty()}
						<EmptyState
							icon={DatabaseIcon}
							title="No databases in this project"
							description={canCreateDatabase
								? 'Provision a PostgreSQL database for this project. Credentials are shown once after creation.'
								: 'Project maintainers can create PostgreSQL databases for the project.'}
						>
							{#snippet action()}
								{#if canCreateDatabase}
									<Button variant="primary" href="/databases?new=1&project={p.id}"
										><Plus class="size-4" /> New database</Button
									>
								{/if}
							{/snippet}
						</EmptyState>
					{/snippet}
				</DataTable>
			</Card>
		{:else if tab === 'activity'}
			<Card
				title="Recent activity"
				description="Last 100 events for the project and its resources"
				padded={false}
			>
				{#if activity.error}
					<ErrorState error={activity.error} retry={activity.reload} />
				{:else if !activity.data}
					<div class="p-5"><Skeleton /></div>
				{:else if activity.data.length === 0}
					<EmptyState icon={History} title="No activity yet" />
				{:else}
					<ul class="divide-y divide-border">
						{#each activity.data as a (a.id)}
							<li class="flex flex-wrap items-center justify-between gap-3 px-5 py-2.5 text-sm">
								<span>
									<span class="font-medium">{a.actor_username ?? 'System'}</span>
									<span class="text-fg-muted">·</span>
									{titleCase(a.action.replaceAll('.', ' '))}
									<span class="text-fg-muted">· {a.resource_type}</span>
								</span>
								<span class="flex items-center gap-2 text-xs text-fg-muted">
									{#if a.outcome !== 'success'}<StatusBadge status={a.outcome} />{/if}
									<time datetime={a.occurred_at} title={dateTime(a.occurred_at)}
										>{relative(a.occurred_at)}</time
									>
								</span>
							</li>
						{/each}
					</ul>
				{/if}
			</Card>
		{/if}
	</div>

	<Modal bind:open={editOpen} title="Edit project">
		<form id="edit-project" class="space-y-4" onsubmit={saveEdit}>
			{#if editError}<p class="text-sm text-danger" role="alert">{editError}</p>{/if}
			<Field label="Name" id="edit-name" error={editErrors.name} required>
				<input id="edit-name" class="input" bind:value={editName} maxlength="120" required />
			</Field>
			<Field label="Description" id="edit-description" error={editErrors.description}>
				<textarea
					id="edit-description"
					class="input"
					rows="4"
					maxlength="2000"
					bind:value={editDescription}></textarea>
			</Field>
			<p class="text-xs text-fg-muted">
				The slug <span class="kbd">{p.slug}</span> cannot be changed.
			</p>
		</form>
		{#snippet footer()}
			<Button onclick={() => (editOpen = false)}>Cancel</Button>
			<Button
				type="submit"
				form="edit-project"
				variant="primary"
				loading={saving}
				disabled={!editName.trim()}>Save changes</Button
			>
		{/snippet}
	</Modal>
{/if}
