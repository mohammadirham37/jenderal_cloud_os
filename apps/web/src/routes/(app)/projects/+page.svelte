<script lang="ts">
	import { onMount } from 'svelte';
	import { FolderKanban, Plus } from '@lucide/svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		Badge,
		Button,
		Card,
		DataTable,
		EmptyState,
		Field,
		Modal,
		PageHeader,
		Pagination,
		SearchInput,
		StatusBadge,
		Switch
	} from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Project } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { date, titleCase } from '#lib/format.ts';

	const perPage = 20;
	let searchText = $state('');
	let query = $state('');
	let status = $state('');
	let mine = $state(false);
	let pageNo = $state(1);
	let sort = $state('name');

	const canViewAll = $derived(auth.can('projects.view_all', 'projects.manage_all'));

	const list = resource(() =>
		api.list<Project>('/projects', {
			q: query,
			status,
			mine: mine ? 'true' : undefined,
			page: pageNo,
			per_page: perPage,
			sort
		})
	);
	const rows = $derived(list.data?.data ?? []);
	const filtered = $derived(query !== '' || status !== '' || mine);

	const columns = [
		{ key: 'name', label: 'Project', sortable: true },
		{ key: 'owner', label: 'Owner' },
		{ key: 'role', label: 'My role' },
		{ key: 'members', label: 'Members', class: 'text-right' },
		{ key: 'workspaces', label: 'Workspaces', class: 'text-right' },
		{ key: 'databases', label: 'Databases', class: 'text-right' },
		{ key: 'status', label: 'Status' },
		{ key: 'created_at', label: 'Created', sortable: true }
	];

	// Create project
	let createOpen = $state(false);
	let name = $state('');
	let slug = $state('');
	let description = $state('');
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let formError = $state('');

	const slugPattern = '[a-z0-9][a-z0-9\\-]{1,48}[a-z0-9]';
	const slugPreview = $derived(
		slug.trim() ||
			name
				.toLowerCase()
				.replace(/[^a-z0-9]+/g, '-')
				.replace(/^-+|-+$/g, '')
				.slice(0, 50)
	);

	function openCreate() {
		name = '';
		slug = '';
		description = '';
		errors = {};
		formError = '';
		createOpen = true;
	}

	onMount(() => {
		if (page.url.searchParams.get('new') === '1' && auth.can('projects.create')) openCreate();
	});

	async function create(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		errors = {};
		formError = '';
		try {
			const body: Record<string, string> = { name: name.trim(), description: description.trim() };
			if (slug.trim()) body.slug = slug.trim().toLowerCase();
			const p = await api.post<Project>('/projects', body);
			toast.success('Project created', p.name);
			createOpen = false;
			await goto(`/projects/${p.id}`);
		} catch (err) {
			if (err instanceof ApiError && err.status === 422 && Object.keys(err.details).length) {
				errors = err.details;
			} else {
				formError = errorMessage(err);
			}
		} finally {
			saving = false;
		}
	}

	const roleTone = {
		owner: 'accent',
		maintainer: 'info',
		member: 'neutral',
		viewer: 'neutral'
	} as const;
</script>

<PageHeader
	title="Projects"
	description="Group workspaces, databases and people working on the same course, lab or research."
>
	{#snippet actions()}
		{#if auth.can('projects.create')}
			<Button variant="primary" onclick={openCreate}><Plus class="size-4" /> New project</Button>
		{/if}
	{/snippet}
</PageHeader>

<Card padded={false}>
	<div class="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
		<div class="w-full sm:w-72">
			<SearchInput
				bind:value={searchText}
				placeholder="Search projects by name or slug"
				onsearch={(v) => {
					query = v.trim();
					pageNo = 1;
				}}
			/>
		</div>
		<label class="sr-only" for="project-status">Status</label>
		<select
			id="project-status"
			class="input w-auto"
			bind:value={status}
			onchange={() => (pageNo = 1)}
		>
			<option value="">All statuses</option>
			<option value="active">Active</option>
			<option value="archived">Archived</option>
		</select>
		{#if canViewAll}
			<div class="flex items-center gap-2">
				<Switch
					id="projects-mine"
					label="Show only projects I belong to"
					bind:checked={mine}
					onchange={() => (pageNo = 1)}
				/>
				<label for="projects-mine" class="text-sm text-fg-muted">Mine only</label>
			</div>
		{/if}
	</div>

	<DataTable
		{columns}
		{rows}
		loading={list.loading}
		error={list.error}
		retry={list.reload}
		bind:sort
		onsort={() => (pageNo = 1)}
	>
		{#snippet row(p: Project)}
			<tr class="hover:bg-panel-2/50">
				<td class="px-4 py-3">
					<a href="/projects/{p.id}" class="font-medium text-fg hover:underline">{p.name}</a>
					<p class="font-mono text-xs text-fg-muted">{p.slug}</p>
				</td>
				<td class="px-4 py-3 text-fg-muted">{p.owner_username}</td>
				<td class="px-4 py-3">
					{#if p.my_role}
						<Badge tone={roleTone[p.my_role]}>{titleCase(p.my_role)}</Badge>
					{:else}
						<span class="text-xs text-fg-subtle">Not a member</span>
					{/if}
				</td>
				<td class="px-4 py-3 text-right tabular-nums">{p.member_count}</td>
				<td class="px-4 py-3 text-right tabular-nums">{p.workspace_count}</td>
				<td class="px-4 py-3 text-right tabular-nums">{p.database_count}</td>
				<td class="px-4 py-3"><StatusBadge status={p.status} /></td>
				<td class="px-4 py-3 whitespace-nowrap text-fg-muted">{date(p.created_at)}</td>
			</tr>
		{/snippet}
		{#snippet empty()}
			{#if filtered}
				<EmptyState
					icon={FolderKanban}
					title="No matching projects"
					description="Try a different search term or clear the filters."
				/>
			{:else}
				<EmptyState
					icon={FolderKanban}
					title="No projects yet"
					description={auth.can('projects.create')
						? 'Create a project to share workspaces and databases with classmates or colleagues.'
						: 'You are not a member of any project. Ask a project owner to add you.'}
				>
					{#snippet action()}
						{#if auth.can('projects.create')}
							<Button variant="primary" onclick={openCreate}
								><Plus class="size-4" /> New project</Button
							>
						{/if}
					{/snippet}
				</EmptyState>
			{/if}
		{/snippet}
	</DataTable>
	{#if list.data && list.data.meta.total > perPage}
		<div class="border-t border-border px-4">
			<Pagination bind:page={pageNo} {perPage} total={list.data.meta.total} />
		</div>
	{/if}
</Card>

<Modal
	bind:open={createOpen}
	title="New project"
	description="You become the owner and can invite members afterwards."
>
	<form id="create-project" class="space-y-4" onsubmit={create}>
		{#if formError}<p class="text-sm text-danger" role="alert">{formError}</p>{/if}
		<Field label="Name" id="project-name" error={errors.name} required>
			<input
				id="project-name"
				class="input"
				bind:value={name}
				maxlength="120"
				required
				aria-invalid={!!errors.name}
			/>
		</Field>
		<Field
			label="Slug"
			id="project-slug"
			error={errors.slug}
			hint={slugPreview
				? `URL identifier; cannot be changed later. Will be: ${slugPreview}`
				: 'Optional. Generated from the name when empty; cannot be changed later.'}
		>
			<input
				id="project-slug"
				class="input font-mono"
				bind:value={slug}
				maxlength="50"
				placeholder="e.g. machine-learning-lab"
				pattern={slugPattern}
				aria-invalid={!!errors.slug}
			/>
		</Field>
		<Field label="Description" id="project-description" error={errors.description}>
			<textarea
				id="project-description"
				class="input"
				rows="3"
				maxlength="2000"
				bind:value={description}></textarea>
		</Field>
	</form>
	{#snippet footer()}
		<Button onclick={() => (createOpen = false)}>Cancel</Button>
		<Button
			type="submit"
			form="create-project"
			variant="primary"
			loading={saving}
			disabled={!name.trim()}>Create project</Button
		>
	{/snippet}
</Modal>
