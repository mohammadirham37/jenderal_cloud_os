<script lang="ts">
	import { untrack } from 'svelte';
	import { ExternalLink, Layers, Play, Plus, Square } from '@lucide/svelte';
	import { page } from '$app/state';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Workspace } from '#lib/api/types.ts';
	import {
		Button,
		Card,
		DataTable,
		EmptyState,
		PageHeader,
		Pagination,
		SearchInput,
		StatusBadge,
		Switch
	} from '#lib/components/ui/index.ts';
	import ResourceSummary from '#lib/components/workspaces/ResourceSummary.svelte';
	import {
		WORKSPACE_STATUSES,
		canOperate,
		canStart,
		canStop,
		isWorkspaceBusy,
		toastError
	} from '#lib/components/workspaces/workspace.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { dateTime, relative, titleCase } from '#lib/format.ts';

	const perPage = 20;
	let q = $state(page.url.searchParams.get('q') ?? '');
	let search = $state(page.url.searchParams.get('q') ?? '');
	let status = $state(page.url.searchParams.get('status') ?? '');
	let mine = $state(false);
	let pageNo = $state(1);
	let sort = $state('-created_at');
	let busy = $state<Record<string, boolean>>({});

	const canViewAll = $derived(auth.can('workspaces.view_all', 'workspaces.manage_all'));
	const canCreate = $derived(auth.can('workspaces.create'));

	// The resource re-fetches whenever the reactive query values read here change.
	const list = resource(() =>
		api.list<Workspace>('/workspaces', {
			q: search,
			status,
			mine: mine ? 'true' : undefined,
			page: pageNo,
			per_page: perPage,
			sort
		})
	);
	const rows = $derived(list.data?.data ?? []);
	const anyBusy = $derived(rows.some(isWorkspaceBusy));

	$effect(() => {
		const ms = anyBusy ? 5000 : 30000;
		const t = setInterval(() => {
			if (document.visibilityState === 'visible') untrack(() => list.reload());
		}, ms);
		return () => clearInterval(t);
	});

	const filtered = $derived(search !== '' || status !== '' || mine);

	const columns = [
		{ key: 'name', label: 'Name', sortable: true },
		{ key: 'owner', label: 'Owner' },
		{ key: 'template', label: 'Template' },
		{ key: 'status', label: 'Status', sortable: true },
		{ key: 'resources', label: 'Resources' },
		{ key: 'project', label: 'Project' },
		{ key: 'created_at', label: 'Created', sortable: true },
		{ key: 'actions', label: 'Actions', class: 'text-right' }
	];

	async function act(w: Workspace, action: 'start' | 'stop') {
		if (action === 'stop') {
			const ok = await confirmDialog.confirm({
				title: `Stop ${w.name}?`,
				message:
					'Running processes and open terminals in this workspace will be stopped. Files in the workspace volume are kept.',
				confirmLabel: 'Stop workspace',
				danger: true
			});
			if (!ok) return;
		}
		busy[w.id] = true;
		try {
			await api.post<Workspace>(`/workspaces/${w.id}/${action}`);
			toast.success(
				action === 'start' ? 'Starting workspace' : 'Stopping workspace',
				`${w.name} will be ${action === 'start' ? 'running' : 'stopped'} shortly.`
			);
			await list.reload();
		} catch (e) {
			toastError(`Could not ${action} ${w.name}`, e);
		} finally {
			busy[w.id] = false;
		}
	}

	function resetPage() {
		pageNo = 1;
	}
</script>

<PageHeader title="Workspaces" description="Your development environments and their status.">
	{#snippet actions()}
		{#if canCreate}
			<Button href="/workspaces/new" variant="primary"><Plus class="size-4" /> New workspace</Button
			>
		{/if}
	{/snippet}
</PageHeader>

<Card padded={false}>
	<div class="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
		<div class="w-full sm:w-72">
			<SearchInput
				bind:value={q}
				placeholder="Search name, slug or owner"
				onsearch={(v) => {
					search = v.trim();
					resetPage();
				}}
			/>
		</div>
		<label class="sr-only" for="ws-status">Status</label>
		<select id="ws-status" class="input w-auto" bind:value={status} onchange={resetPage}>
			<option value="">All statuses</option>
			{#each WORKSPACE_STATUSES as s (s)}<option value={s}>{titleCase(s)}</option>{/each}
		</select>
		{#if canViewAll}
			<div class="flex items-center gap-2 text-sm text-fg-muted">
				<Switch id="ws-mine" label="Mine only" bind:checked={mine} onchange={resetPage} />
				<label for="ws-mine">Mine only</label>
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
		onsort={resetPage}
	>
		{#snippet row(w: Workspace)}
			<tr class="hover:bg-panel-2/40">
				<td class="px-4 py-2.5">
					<a href="/workspaces/{w.id}" class="font-medium text-fg hover:underline">{w.name}</a>
					<p class="font-mono text-xs text-fg-subtle">{w.slug}</p>
				</td>
				<td class="px-4 py-2.5 text-fg-muted">{w.owner_username}</td>
				<td class="px-4 py-2.5 text-fg-muted">{w.template_name}</td>
				<td class="px-4 py-2.5">
					<StatusBadge status={w.status} pending={w.pending_job} />
					{#if w.type === 'native'}<span class="ml-1 text-xs text-fg-subtle">native</span>{/if}
				</td>
				<td class="px-4 py-2.5">
					<ResourceSummary cpu={w.cpu_millis} memory={w.memory_bytes} storage={w.storage_bytes} />
				</td>
				<td class="px-4 py-2.5">
					{#if w.project_id}
						<a href="/projects/{w.project_id}" class="link text-sm">{w.project_name}</a>
					{:else}<span class="text-fg-subtle">—</span>{/if}
				</td>
				<td class="px-4 py-2.5 whitespace-nowrap text-fg-muted" title={dateTime(w.created_at)}>
					{relative(w.created_at)}
				</td>
				<td class="px-4 py-2.5">
					<div class="flex justify-end gap-1.5">
						{#if canOperate(w.access) && w.pending_job === null}
							{#if canStart(w.status)}
								<Button
									size="sm"
									loading={busy[w.id]}
									onclick={() => act(w, 'start')}
									aria-label="Start {w.name}"><Play class="size-3.5" /> Start</Button
								>
							{:else if canStop(w.status)}
								<Button
									size="sm"
									loading={busy[w.id]}
									onclick={() => act(w, 'stop')}
									aria-label="Stop {w.name}"><Square class="size-3.5" /> Stop</Button
								>
							{/if}
						{/if}
						<Button size="sm" variant="ghost" href="/workspaces/{w.id}" aria-label="Open {w.name}">
							<ExternalLink class="size-3.5" />
						</Button>
					</div>
				</td>
			</tr>
		{/snippet}
		{#snippet empty()}
			{#if filtered}
				<EmptyState
					title="No matching workspaces"
					description="No workspace matches the current search or filters."
				>
					{#snippet action()}
						<Button
							onclick={() => {
								q = '';
								search = '';
								status = '';
								mine = false;
								resetPage();
							}}>Clear filters</Button
						>
					{/snippet}
				</EmptyState>
			{:else}
				<EmptyState
					icon={Layers}
					title="No workspaces yet"
					description={canCreate
						? 'Create a workspace from a template to get a ready-to-use development environment.'
						: 'You do not have any workspaces. Ask an administrator for access or to be added to a project.'}
				>
					{#snippet action()}
						{#if canCreate}
							<Button href="/workspaces/new" variant="primary"
								><Plus class="size-4" /> Create workspace</Button
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
