<script lang="ts">
	import { onMount, untrack } from 'svelte';
	import { page } from '$app/state';
	import {
		FolderOpen,
		Layers,
		Plug,
		Plus,
		RefreshCw,
		ShieldAlert,
		SquareTerminal,
		Trash2,
		X
	} from '@lucide/svelte';
	import {
		Alert,
		Badge,
		Button,
		Card,
		EmptyState,
		ErrorState,
		PageHeader,
		Skeleton,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { TerminalSession, Workspace, WorkspaceDetail } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { appConfig } from '#lib/stores/config.svelte.ts';
	import { dateTime, relative } from '#lib/format.ts';
	import TerminalView, { type ConnState } from '#lib/components/terminal/TerminalView.svelte';
	import {
		ACCESS_REASON_HEADER,
		askEmergencyReason,
		isEmergencyRequired
	} from '#lib/components/files/emergency.ts';

	interface Tab {
		session: TerminalSession;
		state: ConnState;
		ended: boolean;
		resumed: boolean;
	}

	const canOpen = $derived(auth.can('terminals.open'));
	const requested = $derived(page.url.searchParams.get('workspace') ?? '');

	// ---- Data ----------------------------------------------------------------
	const workspaces = resource(() =>
		api.list<Workspace>('/workspaces', { status: 'running', per_page: 100, sort: 'name' })
	);
	const sessions = resource(() => api.get<TerminalSession[]>('/terminals'), { poll: 30000 });

	const usable = $derived(
		(workspaces.data?.data ?? []).filter(
			(w) => w.access === 'owner' || w.access === 'editor' || auth.can('workspaces.access_all')
		)
	);
	let now = $state(Date.now());
	const openSessions = $derived(
		(sessions.data ?? []).filter(
			(s) =>
				(s.status === 'pending' || s.status === 'active') && new Date(s.expires_at).getTime() > now
		)
	);
	const requestedMissing = $derived(
		!!requested && !!workspaces.data && !usable.some((w) => w.id === requested)
	);

	let wsId = $state('');
	let containerId = $state('');
	let detail = $state<WorkspaceDetail | null>(null);
	let detailLoading = $state(false);
	let detailError = $state<unknown>(null);
	const running = $derived(detail?.containers.filter((c) => c.status === 'running') ?? []);
	const selected = $derived(usable.find((w) => w.id === wsId));
	/** Another user's workspace reached only through workspaces.access_all. */
	const isForeign = (w: Workspace) => w.owner_id !== auth.me?.id && w.access !== 'editor';
	const emergency = $derived(!!selected && isForeign(selected));

	// Preselect ?workspace=<id> or the only running workspace.
	$effect(() => {
		const list = usable;
		const want = requested;
		untrack(() => {
			if (wsId && list.some((w) => w.id === wsId)) return;
			if (want && list.some((w) => w.id === want)) wsId = want;
			else if (list.length === 1) wsId = list[0].id;
			else wsId = '';
		});
	});

	let detailSeq = 0;
	async function loadDetail(id: string) {
		const my = ++detailSeq;
		detail = null;
		detailError = null;
		containerId = '';
		if (!id) return;
		detailLoading = true;
		try {
			const d = await api.get<WorkspaceDetail>(`/workspaces/${id}`);
			if (my !== detailSeq) return;
			detail = d;
			const run = d.containers.filter((c) => c.status === 'running');
			containerId = (run.find((c) => c.is_primary) ?? run[0])?.id ?? '';
		} catch (e) {
			if (my === detailSeq) detailError = e;
		} finally {
			if (my === detailSeq) detailLoading = false;
		}
	}

	$effect(() => {
		const id = wsId;
		untrack(() => loadDetail(id));
	});

	onMount(() => {
		appConfig.load();
		const t = setInterval(() => (now = Date.now()), 30000);
		return () => clearInterval(t);
	});

	// ---- Sessions ------------------------------------------------------------
	let tabs = $state<Tab[]>([]);
	let activeId = $state<string | null>(null);
	let opening = $state(false);
	let openError = $state<ApiError | Error | null>(null);
	/** Emergency access reasons per workspace (memory only, for restarts). */
	const reasons: Record<string, string> = {};

	function estimateSize() {
		const cols = Math.floor((window.innerWidth - 120) / 8.5);
		const rows = Math.floor((window.innerHeight * 0.6) / 18);
		return {
			cols: Math.min(300, Math.max(40, cols)),
			rows: Math.min(120, Math.max(10, rows))
		};
	}

	async function createSession(
		workspaceId: string,
		container: string | null
	): Promise<TerminalSession | null> {
		const body = { container_id: container || undefined, ...estimateSize() };
		const post = () => {
			const r = reasons[workspaceId];
			return api.post<TerminalSession>(
				`/workspaces/${workspaceId}/terminals`,
				body,
				r ? { headers: { [ACCESS_REASON_HEADER]: r } } : undefined
			);
		};
		try {
			return await post();
		} catch (e) {
			if (!isEmergencyRequired(e)) throw e;
			delete reasons[workspaceId];
			const r = await askEmergencyReason('open a terminal in it');
			if (!r) return null;
			reasons[workspaceId] = r;
			return post();
		}
	}

	function addTab(s: TerminalSession, resumed: boolean) {
		if (!tabs.some((t) => t.session.id === s.id)) {
			tabs.push({ session: s, state: { kind: 'connecting' }, ended: false, resumed });
		}
		activeId = s.id;
	}

	async function openNew() {
		if (!wsId) return;
		opening = true;
		openError = null;
		try {
			const s = await createSession(wsId, detail?.type === 'container' ? containerId : null);
			if (s) {
				addTab(s, false);
				sessions.reload();
			}
		} catch (e) {
			openError = e instanceof Error ? e : new Error(errorMessage(e));
		} finally {
			opening = false;
		}
	}

	async function restartTab(old: TerminalSession) {
		try {
			const s = await createSession(old.workspace_id, old.container_id);
			if (!s) return;
			const i = tabs.findIndex((t) => t.session.id === old.id);
			const tab: Tab = { session: s, state: { kind: 'connecting' }, ended: false, resumed: false };
			if (i >= 0) tabs[i] = tab;
			else tabs.push(tab);
			activeId = s.id;
			sessions.reload();
		} catch (e) {
			toast.error('Could not open a new session', errorMessage(e));
		}
	}

	function closeTab(id: string) {
		const i = tabs.findIndex((t) => t.session.id === id);
		if (i < 0) return;
		tabs.splice(i, 1);
		if (activeId === id) activeId = tabs[Math.min(i, tabs.length - 1)]?.session.id ?? null;
	}

	async function closeSession(s: TerminalSession) {
		const ok = await confirmDialog.confirm({
			title: 'Close terminal session?',
			message: `The shell in ${s.workspace_name}${s.container_name ? ` / ${s.container_name}` : ''} is terminated. Running foreground programs in it are stopped.`,
			confirmLabel: 'Close session',
			danger: true
		});
		if (!ok) return;
		try {
			await api.del(`/terminals/${s.id}`);
			toast.success('Terminal session closed');
		} catch (e) {
			if (!(e instanceof ApiError && e.status === 404)) {
				toast.error('Could not close the session', errorMessage(e));
				return;
			}
		}
		closeTab(s.id);
		sessions.reload();
	}

	function tabTone(t: Tab): string {
		if (t.ended) return 'text-fg-subtle';
		switch (t.state.kind) {
			case 'connected':
				return 'text-success';
			case 'connecting':
				return 'text-info';
			case 'reconnecting':
				return 'text-warning';
			default:
				return 'text-danger';
		}
	}

	function tabKeydown(e: KeyboardEvent, i: number) {
		if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
		e.preventDefault();
		const next = (i + (e.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
		activeId = tabs[next].session.id;
		document.getElementById(`term-tab-${activeId}`)?.focus();
	}

	const label = (s: TerminalSession) =>
		s.container_name ? `${s.workspace_name} / ${s.container_name}` : s.workspace_name;
	const ttl = $derived(appConfig.value?.terminal_ttl_minutes);
</script>

<PageHeader
	title="Terminal"
	description="Open a shell in a running workspace. Terminal content is never stored.{ttl
		? ` Sessions expire after ${ttl} minutes.`
		: ''}"
>
	{#snippet actions()}
		{#if canOpen}
			<Button
				variant="ghost"
				onclick={() => {
					workspaces.reload();
					sessions.reload();
				}}
				loading={workspaces.loading || sessions.loading}
			>
				{#if !(workspaces.loading || sessions.loading)}<RefreshCw class="size-4" />{/if} Refresh
			</Button>
		{/if}
	{/snippet}
</PageHeader>

{#if !canOpen}
	<Card>
		<EmptyState
			icon={SquareTerminal}
			title="Terminal access is not available"
			description="Your role does not include permission to open terminals. Contact an administrator if you need shell access."
		/>
	</Card>
{:else}
	<Card padded={false}>
		{#if workspaces.error}
			<ErrorState error={workspaces.error} retry={workspaces.reload} />
		{:else if !workspaces.data}
			<div class="p-5"><Skeleton lines={2} /></div>
		{:else if usable.length === 0}
			<EmptyState
				icon={Layers}
				title="No running workspace"
				description="A terminal needs a running workspace that you own or edit. Start a workspace first, then come back here."
			>
				{#snippet action()}<Button href="/workspaces" variant="primary">Go to workspaces</Button
					>{/snippet}
			</EmptyState>
		{:else}
			<form
				class="flex flex-wrap items-end gap-3 p-4"
				onsubmit={(e) => {
					e.preventDefault();
					openNew();
				}}
			>
				<div class="min-w-56 flex-1">
					<label for="term-workspace" class="label">Workspace</label>
					<select id="term-workspace" class="input" bind:value={wsId}>
						<option value="" disabled>Select a running workspace…</option>
						{#each usable as w (w.id)}
							<option value={w.id}
								>{w.name}{w.owner_id !== auth.me?.id ? ` (${w.owner_username})` : ''}</option
							>
						{/each}
					</select>
				</div>
				{#if detail?.type === 'container' && running.length > 1}
					<div class="min-w-48">
						<label for="term-container" class="label">Container</label>
						<select id="term-container" class="input" bind:value={containerId}>
							{#each running as c (c.id)}
								<option value={c.id}>{c.name}{c.is_primary ? ' (primary)' : ''}</option>
							{/each}
						</select>
					</div>
				{/if}
				<Button
					type="submit"
					variant="primary"
					loading={opening}
					disabled={!wsId ||
						detailLoading ||
						(detail?.type === 'container' && running.length === 0)}
				>
					<SquareTerminal class="size-4" /> Open terminal
				</Button>
			</form>
			<div class="space-y-2 px-4 pb-4 empty:hidden">
				{#if requestedMissing}
					<Alert tone="warning" title="Workspace not available">
						The workspace in the link is not running or you do not have terminal access to it. Start
						it from <a href="/workspaces/{requested}">its page</a> or pick another workspace.
					</Alert>
				{/if}
				{#if emergency}
					<Alert tone="warning" title="Emergency access">
						This workspace belongs to {selected?.owner_username}. Opening a terminal requires a
						justification and is recorded in the audit log.
					</Alert>
				{/if}
				{#if detailError}
					<Alert tone="danger" title="Could not load the workspace"
						>{errorMessage(detailError)}</Alert
					>
				{:else if detail?.type === 'container' && running.length === 0}
					<Alert tone="warning" title="No running container">
						This workspace has no running container yet. Wait for it to start or check
						<a href="/workspaces/{detail.id}">the workspace page</a>.
					</Alert>
				{/if}
				{#if openError}
					{#if openError instanceof ApiError && openError.code === 'quota_exceeded'}
						<Alert tone="danger" title="Terminal session limit reached">
							{openError.message} Close an open session below to free a slot.
						</Alert>
					{:else}
						<Alert tone="danger" title="Could not open a terminal">{openError.message}</Alert>
					{/if}
				{/if}
			</div>
		{/if}
	</Card>

	<section class="mt-6" aria-label="Terminal tabs">
		{#if tabs.length === 0}
			<div class="card">
				<EmptyState
					icon={SquareTerminal}
					title="No terminal open"
					description={openSessions.length > 0
						? 'Open a new terminal above, or attach to one of your open sessions below.'
						: 'Choose a workspace above and select “Open terminal”. You can keep several terminals open as tabs.'}
				/>
			</div>
		{:else}
			<div class="flex items-end gap-1 overflow-x-auto border-b border-border" role="tablist">
				{#each tabs as t, i (t.session.id)}
					<div
						class="-mb-px flex items-center rounded-t-md border border-b-0 {activeId ===
						t.session.id
							? 'border-border bg-panel'
							: 'border-transparent'}"
					>
						<button
							id="term-tab-{t.session.id}"
							role="tab"
							type="button"
							aria-selected={activeId === t.session.id}
							aria-controls="term-panel-{t.session.id}"
							tabindex={activeId === t.session.id ? 0 : -1}
							class="flex items-center gap-2 py-2 pr-1 pl-3 text-sm whitespace-nowrap {activeId ===
							t.session.id
								? 'text-fg'
								: 'text-fg-muted hover:text-fg'}"
							onclick={() => (activeId = t.session.id)}
							onkeydown={(e) => tabKeydown(e, i)}
						>
							<span class="size-2 rounded-full bg-current {tabTone(t)}" aria-hidden="true"></span>
							{label(t.session)}
						</button>
						<button
							type="button"
							class="mr-1 rounded p-1 text-fg-subtle hover:bg-panel-2 hover:text-fg"
							onclick={() => closeTab(t.session.id)}
							aria-label="Hide tab {label(t.session)} (the session stays open)"
							title="Hide tab — the session stays open until you close it or it expires"
						>
							<X class="size-3.5" />
						</button>
					</div>
				{/each}
			</div>

			{#each tabs as t (t.session.id)}
				<div
					id="term-panel-{t.session.id}"
					role="tabpanel"
					aria-labelledby="term-tab-{t.session.id}"
					class={activeId === t.session.id ? 'pt-3' : 'hidden'}
				>
					<div class="mb-2 flex flex-wrap items-center justify-between gap-2 text-sm">
						<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-fg-muted">
							<a href="/workspaces/{t.session.workspace_id}" class="link font-medium"
								>{t.session.workspace_name}</a
							>
							{#if t.session.container_name}<span>Container: {t.session.container_name}</span>{/if}
							<span title={dateTime(t.session.expires_at)}>
								{t.ended ? 'Session ended' : `Expires ${relative(t.session.expires_at, now)}`}
							</span>
						</div>
						<div class="flex gap-2">
							<Button size="sm" variant="ghost" href="/workspaces/{t.session.workspace_id}/files"
								><FolderOpen class="size-3.5" /> Files</Button
							>
							{#if !t.ended}
								<Button size="sm" variant="danger" onclick={() => closeSession(t.session)}>
									<Trash2 class="size-3.5" /> Close session
								</Button>
							{/if}
						</div>
					</div>
					<TerminalView
						session={t.session}
						active={activeId === t.session.id}
						resumed={t.resumed}
						onstatus={(s) => (t.state = s)}
						onended={() => {
							t.ended = true;
							sessions.reload();
						}}
						onrestart={() => restartTab(t.session)}
					/>
				</div>
			{/each}
		{/if}
	</section>

	<Card
		title="Open sessions"
		description="Sessions stay open (without a running shell) until they expire or you close them. Attaching starts a fresh shell."
		class="mt-6"
		padded={false}
	>
		{#if sessions.error}
			<ErrorState error={sessions.error} retry={sessions.reload} />
		{:else if !sessions.data}
			<div class="p-5"><Skeleton lines={2} /></div>
		{:else if openSessions.length === 0}
			<EmptyState
				icon={Plug}
				title="No open sessions"
				description="Terminal sessions you open appear here so you can reattach after closing a tab or reloading the page."
			/>
		{:else}
			<div class="overflow-x-auto">
				<table class="w-full text-left text-sm">
					<thead
						class="border-b border-border bg-panel-2/60 text-xs tracking-wide text-fg-muted uppercase"
					>
						<tr>
							<th scope="col" class="px-4 py-2.5 font-medium">Workspace</th>
							<th scope="col" class="px-4 py-2.5 font-medium">Status</th>
							<th scope="col" class="px-4 py-2.5 font-medium">Opened</th>
							<th scope="col" class="px-4 py-2.5 font-medium">Expires</th>
							<th scope="col" class="px-4 py-2.5 text-right font-medium">Actions</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-border">
						{#each openSessions as s (s.id)}
							{@const inTab = tabs.some((t) => t.session.id === s.id)}
							<tr>
								<td class="px-4 py-2.5">
									<span class="font-medium">{s.workspace_name}</span>
									{#if s.container_name}<span class="text-fg-muted">
											/ {s.container_name}</span
										>{/if}
									{#if inTab}<span class="ml-2"><Badge tone="accent">In a tab</Badge></span>{/if}
								</td>
								<td class="px-4 py-2.5">
									<StatusBadge status={s.status === 'pending' ? 'idle' : s.status} />
								</td>
								<td class="px-4 py-2.5 text-fg-muted" title={dateTime(s.created_at)}
									>{relative(s.created_at, now)}</td
								>
								<td class="px-4 py-2.5 text-fg-muted" title={dateTime(s.expires_at)}
									>{relative(s.expires_at, now)}</td
								>
								<td class="px-4 py-2.5">
									<div class="flex justify-end gap-2">
										<Button size="sm" onclick={() => addTab(s, !!s.connected_at)}>
											{#if inTab}Show{:else}<Plus class="size-3.5" /> Attach{/if}
										</Button>
										<Button size="sm" variant="ghost" onclick={() => closeSession(s)}>
											<Trash2 class="size-3.5 text-danger" /> Close
										</Button>
									</div>
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</Card>

	{#if usable.some(isForeign)}
		<p class="mt-3 flex items-center gap-1.5 text-xs text-fg-muted">
			<ShieldAlert class="size-3.5" aria-hidden="true" /> Workspaces of other users are listed because
			you have emergency access; using them requires a reason and is audited.
		</p>
	{/if}
{/if}
