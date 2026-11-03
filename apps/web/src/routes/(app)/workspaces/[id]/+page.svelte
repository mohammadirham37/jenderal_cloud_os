<script lang="ts">
	import { untrack } from 'svelte';
	import { ShieldAlert } from '@lucide/svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Workspace, WorkspaceDetail } from '#lib/api/types.ts';
	import {
		Alert,
		Badge,
		Card,
		ErrorState,
		PageHeader,
		Skeleton,
		StatusBadge,
		Tabs
	} from '#lib/components/ui/index.ts';
	import WorkspaceActions from '#lib/components/workspaces/WorkspaceActions.svelte';
	import WorkspaceActivity from '#lib/components/workspaces/WorkspaceActivity.svelte';
	import WorkspaceContainers from '#lib/components/workspaces/WorkspaceContainers.svelte';
	import WorkspaceMembers from '#lib/components/workspaces/WorkspaceMembers.svelte';
	import WorkspaceOverview from '#lib/components/workspaces/WorkspaceOverview.svelte';
	import WorkspaceSettings from '#lib/components/workspaces/WorkspaceSettings.svelte';
	import { TRANSIENT_CTR, isWorkspaceBusy } from '#lib/components/workspaces/workspace.ts';
	import { crumbs } from '#lib/stores/breadcrumb.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { titleCase } from '#lib/format.ts';

	const id = $derived(page.params.id ?? '');
	const detail = resource(() => api.get<WorkspaceDetail>(`/workspaces/${id}`));
	const ws = $derived(detail.data?.id === id ? detail.data : undefined);

	/** Set when this page saw the workspace being deleted, so a 404 means "done". */
	let deleting = $state(false);

	const fast = $derived(
		!!ws &&
			(isWorkspaceBusy(ws) ||
				ws.containers.some((c) => c.pending_job !== null || TRANSIENT_CTR.includes(c.status)))
	);
	$effect(() => {
		const ms = fast ? 3000 : 20000;
		const t = setInterval(() => {
			if (document.visibilityState === 'visible') untrack(() => detail.reload());
		}, ms);
		return () => clearInterval(t);
	});

	$effect(() => {
		if (ws) {
			crumbs.set(ws.id, ws.name);
			if (ws.status === 'deleting') deleting = true;
		}
	});

	const notFound = $derived(detail.error instanceof ApiError && detail.error.status === 404);
	$effect(() => {
		if (notFound && deleting) {
			toast.success('Workspace deleted', 'The workspace and its data have been removed.');
			goto('/workspaces');
		}
	});

	let tab = $state(page.url.searchParams.get('tab') ?? 'overview');
	const tabs = $derived(
		ws
			? [
					{ id: 'overview', label: 'Overview' },
					{ id: 'containers', label: 'Containers', count: ws.containers.length },
					{ id: 'members', label: 'Members', count: ws.members.length + 1 },
					...(ws.grants.Manage ? [{ id: 'settings', label: 'Settings' }] : []),
					{ id: 'activity', label: 'Activity' }
				]
			: []
	);
	$effect(() => {
		if (ws && !tabs.some((t) => t.id === tab)) tab = 'overview';
	});

	function merge(w: Workspace) {
		if (ws) detail.set({ ...ws, ...w });
		detail.reload();
	}

	const accessLabel: Record<string, string> = {
		owner: 'Owner access',
		editor: 'Editor access',
		operator: 'Operator access',
		viewer: 'Read-only access'
	};
</script>

{#if detail.error && !(notFound && deleting)}
	<PageHeader title="Workspace" />
	<Card><ErrorState error={detail.error} retry={detail.reload} /></Card>
{:else if !ws}
	<PageHeader title="Workspace" />
	<Card><Skeleton lines={6} /></Card>
{:else}
	<PageHeader title={ws.name}>
		{#snippet meta()}
			<StatusBadge status={ws.status} pending={ws.pending_job} />
			<span>Owner <strong class="text-fg">{ws.owner_username}</strong></span>
			<span aria-hidden="true">·</span>
			<span>{ws.template_name}</span>
			{#if ws.project_id}
				<span aria-hidden="true">·</span>
				<a href="/projects/{ws.project_id}" class="link">{ws.project_name}</a>
			{/if}
			<span aria-hidden="true">·</span>
			<span class="font-mono text-xs">{ws.slug}</span>
			{#if ws.type === 'native'}<Badge tone="warning">Linux-native</Badge>{/if}
			{#if ws.access && accessLabel[ws.access]}
				<Badge tone="neutral">{accessLabel[ws.access]}</Badge>
			{/if}
			{#if ws.grants.Emergency}
				<Badge tone="danger"
					><ShieldAlert class="size-3" aria-hidden="true" /> Emergency access</Badge
				>
			{/if}
		{/snippet}
		{#snippet actions()}
			<div>
				<WorkspaceActions
					{ws}
					onupdate={merge}
					ondeleting={(w) => {
						deleting = true;
						merge(w);
					}}
				/>
			</div>
		{/snippet}
	</PageHeader>

	{#if ws.status_reason}
		<div class="mb-4">
			<Alert
				tone={ws.status === 'failed' ? 'danger' : ws.status === 'suspended' ? 'warning' : 'info'}
				title={ws.status === 'failed'
					? 'Workspace failed'
					: ws.status === 'suspended'
						? 'Workspace suspended'
						: titleCase(ws.status)}
			>
				{ws.status_reason}
			</Alert>
		</div>
	{/if}
	{#if ws.grants.Emergency}
		<div class="mb-4">
			<Alert tone="warning" title="Emergency access">
				You can reach this user's data only through audited emergency access. Opening the terminal,
				files or apps requires a written justification.
			</Alert>
		</div>
	{/if}
	{#if ws.status === 'requested' || ws.status === 'provisioning'}
		<div class="mb-4">
			<Alert tone="info" title="Provisioning">
				The workspace is being prepared (volume, image and container). This page updates
				automatically.
			</Alert>
		</div>
	{/if}

	<div class="mb-6"><Tabs {tabs} bind:active={tab} /></div>

	<div role="tabpanel" aria-labelledby="tab-{tab}">
		{#if tab === 'overview'}
			<WorkspaceOverview {ws} />
		{:else if tab === 'containers'}
			<WorkspaceContainers {ws} onchange={detail.reload} />
		{:else if tab === 'members'}
			<WorkspaceMembers {ws} onchange={detail.reload} />
		{:else if tab === 'settings' && ws.grants.Manage}
			{#key ws.id}
				<WorkspaceSettings {ws} onsaved={(d) => detail.set(d)} />
			{/key}
		{:else if tab === 'activity'}
			<WorkspaceActivity id={ws.id} />
		{/if}
	</div>
{/if}
