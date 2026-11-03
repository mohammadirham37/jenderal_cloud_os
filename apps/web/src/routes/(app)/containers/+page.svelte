<script lang="ts">
	import { untrack } from 'svelte';
	import { Boxes, Play, RefreshCw, Square } from '@lucide/svelte';
	import { page } from '$app/state';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Container } from '#lib/api/types.ts';
	import {
		Badge,
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
	import {
		CONTAINER_STATUSES,
		TRANSIENT_CTR,
		containerAction
	} from '#lib/components/workspaces/workspace.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { bytes, cores, dateTime, relative, titleCase } from '#lib/format.ts';

	const perPage = 20;
	let q = $state(page.url.searchParams.get('q') ?? '');
	let search = $state(page.url.searchParams.get('q') ?? '');
	let status = $state(page.url.searchParams.get('status') ?? '');
	let mine = $state(false);
	let pageNo = $state(1);
	let sort = $state('-created_at');
	let busy = $state<Record<string, boolean>>({});

	const canViewAll = $derived(
		auth.can('containers.manage_all', 'workspaces.view_all', 'workspaces.manage_all')
	);
	const canAct = $derived(auth.can('containers.stop', 'containers.manage_all'));

	// Re-fetches whenever the reactive query values read here change.
	const list = resource(() =>
		api.list<Container>('/containers', {
			q: search,
			status,
			mine: mine ? 'true' : undefined,
			page: pageNo,
			per_page: perPage,
			sort
		})
	);
	const rows = $derived(list.data?.data ?? []);
	const anyBusy = $derived(
		rows.some((c) => c.pending_job !== null || TRANSIENT_CTR.includes(c.status))
	);
	$effect(() => {
		const ms = anyBusy ? 5000 : 30000;
		const t = setInterval(() => {
			if (document.visibilityState === 'visible') untrack(() => list.reload());
		}, ms);
		return () => clearInterval(t);
	});

	const filtered = $derived(search !== '' || status !== '' || mine);

	const columns = $derived([
		{ key: 'name', label: 'Name', sortable: true },
		{ key: 'workspace', label: 'Workspace' },
		{ key: 'owner', label: 'Owner' },
		{ key: 'image', label: 'Image' },
		{ key: 'status', label: 'Status', sortable: true },
		{ key: 'resources', label: 'CPU / memory' },
		{ key: 'started', label: 'Started' },
		...(canAct ? [{ key: 'actions', label: 'Actions', class: 'text-right' }] : [])
	]);

	async function act(c: Container, action: 'start' | 'stop' | 'restart') {
		busy[c.id] = true;
		const out = await containerAction(c, action);
		busy[c.id] = false;
		if (out) await list.reload();
	}

	function resetPage() {
		pageNo = 1;
	}
</script>

<PageHeader
	title="Containers"
	description="Containers of the workspaces you can access. Each workspace has a primary container and optional extra ones."
/>

<Card padded={false}>
	<div class="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
		<div class="w-full sm:w-72">
			<SearchInput
				bind:value={q}
				placeholder="Search container, workspace, owner or image"
				onsearch={(v) => {
					search = v.trim();
					resetPage();
				}}
			/>
		</div>
		<label class="sr-only" for="ctr-status">Status</label>
		<select id="ctr-status" class="input w-auto" bind:value={status} onchange={resetPage}>
			<option value="">All statuses</option>
			{#each CONTAINER_STATUSES as s (s)}<option value={s}>{titleCase(s)}</option>{/each}
		</select>
		{#if canViewAll}
			<div class="flex items-center gap-2 text-sm text-fg-muted">
				<Switch id="ctr-mine" label="Mine only" bind:checked={mine} onchange={resetPage} />
				<label for="ctr-mine">Mine only</label>
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
		{#snippet row(c: Container)}
			{@const idle = c.pending_job === null && !TRANSIENT_CTR.includes(c.status)}
			<tr class="hover:bg-panel-2/40">
				<td class="px-4 py-2.5">
					<a href="/containers/{c.id}" class="font-medium hover:underline">{c.name}</a>
					{#if c.is_primary}<span class="ml-1"><Badge tone="accent">primary</Badge></span>{/if}
				</td>
				<td class="px-4 py-2.5">
					<a href="/workspaces/{c.workspace_id}" class="link">{c.workspace_name}</a>
				</td>
				<td class="px-4 py-2.5 text-fg-muted">{c.owner_username}</td>
				<td class="max-w-56 truncate px-4 py-2.5 font-mono text-xs text-fg-muted" title={c.image}>
					{c.image}
				</td>
				<td class="px-4 py-2.5"><StatusBadge status={c.status} pending={c.pending_job} /></td>
				<td class="px-4 py-2.5 text-xs whitespace-nowrap text-fg-muted tabular-nums">
					{cores(c.cpu_millis)} · {bytes(c.memory_bytes)}
				</td>
				<td class="px-4 py-2.5 whitespace-nowrap text-fg-muted" title={dateTime(c.started_at)}>
					{c.started_at && c.status === 'running' ? relative(c.started_at) : '—'}
				</td>
				{#if canAct}
					<td class="px-4 py-2.5">
						<div class="flex justify-end gap-1.5">
							{#if idle && c.status === 'running'}
								<Button
									size="sm"
									loading={busy[c.id]}
									onclick={() => act(c, 'restart')}
									aria-label="Restart {c.name}"><RefreshCw class="size-3.5" /></Button
								>
								<Button
									size="sm"
									loading={busy[c.id]}
									onclick={() => act(c, 'stop')}
									aria-label="Stop {c.name}"><Square class="size-3.5" /> Stop</Button
								>
							{:else if idle && ['created', 'stopped', 'failed'].includes(c.status)}
								<Button
									size="sm"
									loading={busy[c.id]}
									onclick={() => act(c, 'start')}
									aria-label="Start {c.name}"><Play class="size-3.5" /> Start</Button
								>
							{/if}
						</div>
					</td>
				{/if}
			</tr>
		{/snippet}
		{#snippet empty()}
			{#if filtered}
				<EmptyState
					title="No matching containers"
					description="No container matches the current search or filters."
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
					icon={Boxes}
					title="No containers yet"
					description="Containers are created together with workspaces."
				>
					{#snippet action()}
						<Button href="/workspaces">Go to workspaces</Button>
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
