<script lang="ts">
	import {
		Boxes,
		Cpu,
		FolderKanban,
		Layers,
		MemoryStick,
		Plus,
		SquareTerminal,
		HardDrive,
		Megaphone
	} from '@lucide/svelte';
	import {
		Alert,
		Button,
		Card,
		EmptyState,
		ErrorState,
		Meter,
		PageHeader,
		Skeleton,
		StatCard,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Announcement, Container, Quota, Usage } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { bytes, cores, relative, titleCase } from '#lib/format.ts';

	interface Summary {
		quota: Quota;
		usage: Usage;
		workspaces_by_status: Record<string, number>;
		recent_activity: {
			id: number;
			occurred_at: string;
			action: string;
			resource_type: string;
			outcome: string;
		}[];
		gpu: { pending_requests: number; active_allocations: number };
		projects: number;
		platform?: {
			active_users: number;
			running_workspaces: number;
			running_containers: number;
			pending_gpu_requests: number;
			firing_alerts: number;
		};
	}

	const summary = resource(() => api.get<Summary>('/dashboard'), { poll: 30000 });
	const containers = resource(
		() => api.list<Container>('/containers', { status: 'running', mine: 'true', per_page: 5 }),
		{ poll: 30000 }
	);
	const announcements = resource(() => api.get<Announcement[]>('/announcements'));

	const hour = new Date().getHours();
	const greeting =
		hour < 11
			? 'Good morning'
			: hour < 15
				? 'Good afternoon'
				: hour < 19
					? 'Good evening'
					: 'Good night';
	const totalWS = $derived(
		Object.values(summary.data?.workspaces_by_status ?? {}).reduce((a, b) => a + b, 0)
	);
	const tone = { info: 'info', warning: 'warning', critical: 'danger' } as const;
</script>

<PageHeader
	title="{greeting}, {auth.me?.full_name.split(' ')[0]}"
	description="Overview of your workspaces, resources and recent activity."
>
	{#snippet actions()}
		{#if auth.can('workspaces.create')}
			<Button href="/workspaces/new" variant="primary"><Plus class="size-4" /> New workspace</Button
			>
		{/if}
	{/snippet}
</PageHeader>

{#if announcements.data?.length}
	<div class="mb-6 space-y-2">
		{#each announcements.data as a (a.id)}
			<Alert tone={tone[a.severity]} title={a.title}>
				<span class="whitespace-pre-line">{a.body}</span>
			</Alert>
		{/each}
	</div>
{/if}

{#if summary.error}
	<Card><ErrorState error={summary.error} retry={summary.reload} /></Card>
{:else if !summary.data}
	<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
		{#each [1, 2, 3, 4] as i (i)}<div class="card p-4"><Skeleton lines={2} /></div>{/each}
	</div>
{:else}
	{@const s = summary.data}
	<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
		<StatCard
			label="Workspaces"
			value={String(totalWS)}
			sub="{s.workspaces_by_status.running ?? 0} running · limit {s.quota.max_workspaces}"
			icon={Layers}
		/>
		<StatCard
			label="CPU in use"
			value={cores(s.usage.running_cpu_millis)}
			sub="of {cores(s.quota.cpu_millis)} quota"
			icon={Cpu}
		>
			<Meter label="CPU quota" value={s.usage.running_cpu_millis} max={s.quota.cpu_millis} />
		</StatCard>
		<StatCard
			label="Memory in use"
			value={bytes(s.usage.running_memory_bytes)}
			sub="of {bytes(s.quota.memory_bytes)} quota"
			icon={MemoryStick}
		>
			<Meter label="Memory quota" value={s.usage.running_memory_bytes} max={s.quota.memory_bytes} />
		</StatCard>
		<StatCard
			label="Storage allocated"
			value={bytes(s.usage.allocated_storage_bytes)}
			sub="{bytes(s.usage.used_storage_bytes)} used · quota {bytes(s.quota.storage_bytes)}"
			icon={HardDrive}
		>
			<Meter
				label="Storage quota"
				value={s.usage.allocated_storage_bytes}
				max={s.quota.storage_bytes}
			/>
		</StatCard>
	</div>

	{#if s.platform}
		<div class="mt-4 grid gap-4 sm:grid-cols-2 xl:grid-cols-5">
			<StatCard label="Active users" value={String(s.platform.active_users)} />
			<StatCard label="Running workspaces" value={String(s.platform.running_workspaces)} />
			<StatCard label="Running containers" value={String(s.platform.running_containers)} />
			<a href="/gpu?tab=approvals" class="block"
				><StatCard
					label="Pending GPU requests"
					value={String(s.platform.pending_gpu_requests)}
				/></a
			>
			<a href="/monitoring?tab=alerts" class="block"
				><StatCard
					label="Firing alerts"
					value={String(s.platform.firing_alerts)}
					sub={s.platform.firing_alerts ? 'Needs attention' : 'All clear'}
				/></a
			>
		</div>
	{/if}

	<div class="mt-6 grid gap-6 lg:grid-cols-3">
		<Card title="Running containers" class="lg:col-span-2" padded={false}>
			{#snippet actions()}<a href="/containers" class="link text-sm">View all</a>{/snippet}
			{#if containers.error}
				<ErrorState error={containers.error} retry={containers.reload} />
			{:else if !containers.data}
				<div class="p-5"><Skeleton /></div>
			{:else if containers.data.data.length === 0}
				<EmptyState
					icon={Boxes}
					title="No running containers"
					description="Start a workspace to run its containers."
				>
					{#snippet action()}<Button href="/workspaces">Go to workspaces</Button>{/snippet}
				</EmptyState>
			{:else}
				<ul class="divide-y divide-border">
					{#each containers.data.data as c (c.id)}
						<li class="flex items-center justify-between gap-3 px-5 py-3">
							<div class="min-w-0">
								<a
									href="/workspaces/{c.workspace_id}"
									class="truncate text-sm font-medium hover:underline"
									>{c.workspace_name} / {c.name}</a
								>
								<p class="truncate text-xs text-fg-muted">
									{c.image} · {cores(c.cpu_millis)} · {bytes(c.memory_bytes)} · up {relative(
										c.started_at
									)}
								</p>
							</div>
							<div class="flex items-center gap-2">
								<StatusBadge status={c.status} />
								{#if auth.can('terminals.open')}
									<Button
										size="sm"
										href="/terminal?workspace={c.workspace_id}"
										title="Open terminal"><SquareTerminal class="size-3.5" /></Button
									>
								{/if}
							</div>
						</li>
					{/each}
				</ul>
			{/if}
		</Card>

		<Card title="Quick actions">
			<div class="grid gap-2">
				{#if auth.can('workspaces.create')}<Button href="/workspaces/new" class="justify-start"
						><Plus class="size-4" /> Create workspace</Button
					>{/if}
				{#if auth.can('terminals.open')}<Button href="/terminal" class="justify-start"
						><SquareTerminal class="size-4" /> Open terminal</Button
					>{/if}
				{#if auth.can('projects.create')}<Button href="/projects?new=1" class="justify-start"
						><FolderKanban class="size-4" /> New project</Button
					>{/if}
				{#if auth.can('gpu.request')}<Button href="/gpu?tab=request" class="justify-start"
						><Cpu class="size-4" /> Request GPU access</Button
					>{/if}
			</div>
			<dl class="mt-4 space-y-1 border-t border-border pt-4 text-sm">
				<div class="flex justify-between">
					<dt class="text-fg-muted">Quota package</dt>
					<dd class="font-medium">{s.quota.name}</dd>
				</div>
				<div class="flex justify-between">
					<dt class="text-fg-muted">Projects</dt>
					<dd>{s.projects}</dd>
				</div>
				<div class="flex justify-between">
					<dt class="text-fg-muted">GPU allocations</dt>
					<dd>{s.gpu.active_allocations} active · {s.gpu.pending_requests} pending</dd>
				</div>
				<div class="flex justify-between">
					<dt class="text-fg-muted">GPU access</dt>
					<dd>{s.quota.gpu_allowed ? 'Allowed by quota' : 'Not in quota'}</dd>
				</div>
			</dl>
		</Card>
	</div>

	<Card title="Recent activity" class="mt-6" padded={false}>
		{#if s.recent_activity.length === 0}
			<EmptyState
				icon={Megaphone}
				title="No activity yet"
				description="Your actions (creating workspaces, opening terminals, etc.) will appear here."
			/>
		{:else}
			<ul class="divide-y divide-border">
				{#each s.recent_activity as a (a.id)}
					<li class="flex items-center justify-between gap-3 px-5 py-2.5 text-sm">
						<span
							>{titleCase(a.action.replace('.', ' '))}
							<span class="text-fg-muted">· {a.resource_type}</span></span
						>
						<span class="flex items-center gap-2 text-xs text-fg-muted">
							{#if a.outcome !== 'success'}<StatusBadge status={a.outcome} />{/if}{relative(
								a.occurred_at
							)}
						</span>
					</li>
				{/each}
			</ul>
		{/if}
	</Card>
{/if}
