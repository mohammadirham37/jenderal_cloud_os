<script lang="ts">
	// Hosts a built-in workspace app (VS Code or the remote web browser) in a
	// full-size frame served from the isolated app-proxy origin. It picks (or
	// creates) a suitable workspace, starts it when needed and waits until the
	// app's port is published before loading the frame.
	import { onDestroy } from 'svelte';
	import { ExternalLink, Play, Plus, RefreshCw } from '@lucide/svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, errorMessage, idempotencyKey } from '#lib/api/client.ts';
	import type { Template, Workspace, WorkspaceDetail } from '#lib/api/types.ts';
	import {
		Button,
		EmptyState,
		ErrorState,
		Spinner,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { appConfig } from '#lib/stores/config.svelte.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { isEmbedded } from '#lib/desktop/embed.ts';

	interface Props {
		kind: 'vscode' | 'desktop';
		title: string;
		/** Icon shown in empty states. */
		icon: import('svelte').Component;
	}
	let { kind, title, icon }: Props = $props();

	const PORT = $derived(kind === 'vscode' ? 13337 : 6080);
	const BROWSER_TEMPLATE = 'browser';
	const embedded = isEmbedded();

	let templates = $state<Template[] | null>(null);
	let workspaces = $state<Workspace[] | null>(null);
	let loadError = $state<unknown>(null);
	let busy = $state(false);
	let actionError = $state<string | null>(null);
	let ready = $state(false);
	let frameKey = $state(0);
	let timer: ReturnType<typeof setTimeout> | undefined;
	/** Start the workspace as soon as provisioning finishes (after "Create"). */
	let autoStart = false;

	const proxyOrigin = $derived((appConfig.value?.app_proxy_origin ?? '').replace(/\/+$/, ''));

	const capable = $derived.by(() => {
		if (!templates) return new Set<string>();
		return new Set(
			templates
				.filter((t) =>
					kind === 'desktop'
						? t.key === BROWSER_TEMPLATE
						: t.key !== BROWSER_TEMPLATE &&
							(t.ports ?? []).some((p) => p.port === PORT && p.name === 'vscode')
				)
				.map((t) => t.key)
		);
	});

	const candidates = $derived(
		(workspaces ?? []).filter(
			(w) =>
				w.type === 'container' &&
				capable.has(w.template_key) &&
				(w.access === 'owner' || w.access === 'editor') &&
				!['deleting', 'deleted', 'failed', 'suspended'].includes(w.status)
		)
	);

	const selectedId = $derived(page.url.searchParams.get('workspace'));
	const current = $derived(
		candidates.find((w) => w.id === selectedId) ??
			(kind === 'desktop' || candidates.length === 1 ? candidates[0] : undefined)
	);
	const browserTemplate = $derived(templates?.find((t) => t.key === BROWSER_TEMPLATE));

	const src = $derived.by(() => {
		if (!current || !proxyOrigin) return '';
		const base = `/api/v1/workspaces/${current.id}/proxy/${PORT}/`;
		if (kind === 'vscode') return `${proxyOrigin}${base}?folder=/home/jenderal`;
		const params = new URLSearchParams({
			autoconnect: '1',
			reconnect: '1',
			reconnect_delay: '1500',
			resize: 'scale',
			show_dot: '1',
			path: `${base.slice(1)}websockify`
		});
		return `${proxyOrigin}${base}vnc.html?${params}`;
	});

	async function load() {
		loadError = null;
		try {
			const [t, w] = await Promise.all([
				api.get<Template[]>('/templates'),
				api.list<Workspace>('/workspaces', { per_page: 100, sort: '-updated_at' })
			]);
			templates = t;
			workspaces = w.data;
		} catch (e) {
			loadError = e;
		}
	}

	/** Polls the selected workspace until it is running and the app port is published. */
	async function waitUntilReady() {
		clearTimeout(timer);
		if (!current) return;
		try {
			const w = await api.get<WorkspaceDetail>('/workspaces/' + current.id);
			workspaces = (workspaces ?? []).map((x) => (x.id === w.id ? { ...x, ...w } : x));
			if (w.status === 'running' && !w.pending_job) {
				const published = (w.containers ?? []).some(
					(c) => c.status === 'running' && c.ports?.some((p) => p.port === PORT && p.host_port)
				);
				if (published) {
					ready = true;
					return;
				}
				actionError =
					kind === 'vscode'
						? 'This workspace was created before VS Code was available, so it has no VS Code port. Create a new workspace (your files can be copied with the Files app) to use VS Code.'
						: 'The browser session is not published. Delete this browser workspace and create it again.';
				return;
			}
			if (autoStart && ['ready', 'stopped'].includes(w.status) && !w.pending_job) {
				autoStart = false;
				await api.post('/workspaces/' + w.id + '/start', undefined, {
					headers: { 'Idempotency-Key': idempotencyKey() }
				});
			} else if (['failed', 'suspended'].includes(w.status)) {
				actionError = w.status_reason ?? `The workspace is ${w.status}.`;
				return;
			}
		} catch (e) {
			actionError = errorMessage(e);
			return;
		}
		timer = setTimeout(waitUntilReady, 2500);
	}

	async function start() {
		if (!current) return;
		busy = true;
		actionError = null;
		try {
			await api.post('/workspaces/' + current.id + '/start', undefined, {
				headers: { 'Idempotency-Key': idempotencyKey() }
			});
			await waitUntilReady();
		} catch (e) {
			actionError = errorMessage(e);
		} finally {
			busy = false;
		}
	}

	async function createBrowser() {
		busy = true;
		actionError = null;
		try {
			const w = await api.post<Workspace>(
				'/workspaces',
				{
					name: 'Browser',
					template_key: BROWSER_TEMPLATE,
					type: 'container',
					// Profile and downloads only: keep the quota for real work.
					storage_bytes: 1024 ** 3
				},
				{ headers: { 'Idempotency-Key': idempotencyKey() } }
			);
			workspaces = [w, ...(workspaces ?? [])];
			autoStart = true;
			toast.success('Your browser is being prepared. This takes a minute the first time.');
			await waitUntilReady();
		} catch (e) {
			actionError = errorMessage(e);
		} finally {
			busy = false;
		}
	}

	function choose(id: string) {
		ready = false;
		actionError = null;
		const u = new URL(page.url.href);
		u.searchParams.set('workspace', id);
		goto(u.pathname + u.search, { replaceState: true });
	}

	// Re-evaluate whenever the selected workspace changes.
	let lastId: string | undefined;
	$effect(() => {
		const id = current?.id;
		if (id === lastId) return;
		lastId = id;
		ready = false;
		actionError = null;
		if (!current) return;
		if (current.status === 'running' || current.pending_job || current.status === 'provisioning') {
			void waitUntilReady();
		}
	});

	$effect(() => {
		void appConfig.load();
		void load();
	});
	onDestroy(() => clearTimeout(timer));
</script>

<svelte:head><title>{title} · {appConfig.name}</title></svelte:head>

<div
	class={embedded
		? 'fixed inset-0 z-10 flex flex-col bg-surface'
		: 'flex h-[calc(100vh-9rem)] min-h-[480px] flex-col overflow-hidden rounded-lg border border-border bg-surface'}
>
	{#if current && ready && src}
		<div
			class="flex h-9 shrink-0 items-center gap-2 border-b border-border bg-panel px-3 text-xs text-fg-muted"
		>
			{#if kind === 'vscode' && candidates.length > 1}
				<label class="sr-only" for="ws-pick">Workspace</label>
				<select
					id="ws-pick"
					class="input h-7 w-auto py-0 text-xs"
					value={current.id}
					onchange={(e) => choose(e.currentTarget.value)}
				>
					{#each candidates as w (w.id)}<option value={w.id}>{w.name}</option>{/each}
				</select>
			{:else}
				<span class="truncate font-medium text-fg">{current.name}</span>
			{/if}
			<span class="ml-auto"></span>
			<button
				type="button"
				class="inline-flex items-center gap-1 rounded px-2 py-1 hover:bg-panel-2 hover:text-fg"
				onclick={() => (frameKey += 1)}
				title="Reload"
			>
				<RefreshCw class="size-3.5" aria-hidden="true" /> Reload
			</button>
			<a
				class="inline-flex items-center gap-1 rounded px-2 py-1 hover:bg-panel-2 hover:text-fg"
				href={src}
				target="_blank"
				rel="noopener"
			>
				<ExternalLink class="size-3.5" aria-hidden="true" /> New tab
			</a>
		</div>
		{#key frameKey}
			<iframe
				{src}
				{title}
				class="min-h-0 w-full flex-1 border-0 bg-black"
				allow="clipboard-read; clipboard-write; fullscreen"
			></iframe>
		{/key}
	{:else}
		<div class="flex flex-1 items-center justify-center overflow-auto p-6">
			<div class="w-full max-w-xl">
				{#if loadError}
					<ErrorState error={loadError} retry={load} />
				{:else if !templates || !workspaces}
					<div class="flex justify-center"><Spinner /></div>
				{:else if !proxyOrigin}
					<EmptyState
						{icon}
						title="{title} is not available"
						description="The workspace app proxy is not configured on this server. Ask an administrator to set APP_PROXY_ORIGIN."
					/>
				{:else if capable.size === 0}
					<EmptyState
						{icon}
						title="{title} is not installed"
						description={kind === 'vscode'
							? 'No workspace template offers VS Code yet. Ask an administrator to upgrade Jenderal Cloud OS.'
							: 'The Web Browser template is not available. Ask an administrator to build the desktop-browser image.'}
					/>
				{:else if !current && kind === 'desktop'}
					<EmptyState
						{icon}
						title="Set up your browser"
						description="A full Firefox browser runs on the server and appears in this window. Your downloads and bookmarks are kept in your own browser workspace."
					>
						{#snippet action()}
							{#if auth.can('workspaces.create') && browserTemplate?.image_present}
								<Button variant="primary" loading={busy} onclick={createBrowser}>
									<Plus class="size-4" /> Create my browser
								</Button>
							{:else if !browserTemplate?.image_present}
								<p class="text-sm text-warning">
									The browser image is not installed on this server yet.
								</p>
							{/if}
						{/snippet}
					</EmptyState>
				{:else if !current}
					{#if candidates.length === 0}
						<EmptyState
							{icon}
							title="No workspace for {title}"
							description="VS Code opens the files of a development workspace (Ubuntu, Python, Node.js, Go, PHP, Web…). Create one first."
						>
							{#snippet action()}
								{#if auth.can('workspaces.create')}<Button href="/workspaces/new" variant="primary"
										>Create workspace</Button
									>{/if}
							{/snippet}
						</EmptyState>
					{:else}
						<h1 class="mb-1 font-display text-lg font-semibold">Open {title}</h1>
						<p class="mb-4 text-sm text-fg-muted">Choose the workspace to work in.</p>
						<ul class="space-y-2">
							{#each candidates as w (w.id)}
								<li>
									<button
										type="button"
										class="card flex w-full items-center justify-between gap-3 p-3 text-left transition hover:border-accent"
										onclick={() => choose(w.id)}
									>
										<span class="min-w-0">
											<span class="block truncate font-medium">{w.name}</span>
											<span class="block truncate text-xs text-fg-muted">{w.template_name}</span>
										</span>
										<StatusBadge status={w.status} pending={w.pending_job} />
									</button>
								</li>
							{/each}
						</ul>
					{/if}
				{:else}
					<div class="card p-6 text-center">
						<p class="font-display text-lg font-semibold">{current.name}</p>
						<div class="mt-2 flex justify-center">
							<StatusBadge status={current.status} pending={current.pending_job} />
						</div>
						{#if actionError}
							<p class="mt-4 text-sm text-danger" role="alert">{actionError}</p>
						{/if}
						{#if ['running', 'provisioning'].includes(current.status) || current.pending_job}
							{#if !actionError}
								<p class="mt-4 flex items-center justify-center gap-2 text-sm text-fg-muted">
									<Spinner size={14} /> Starting {title}…
								</p>
							{/if}
						{:else}
							<p class="mt-4 text-sm text-fg-muted">
								The workspace is {current.status}. Start it to open {title}.
							</p>
							<div class="mt-4 flex justify-center">
								<Button variant="primary" loading={busy} onclick={start}>
									<Play class="size-4" /> Start workspace
								</Button>
							</div>
						{/if}
					</div>
				{/if}
				{#if actionError && !current}
					<p class="mt-4 text-center text-sm text-danger" role="alert">{actionError}</p>
				{/if}
			</div>
		</div>
	{/if}
</div>
