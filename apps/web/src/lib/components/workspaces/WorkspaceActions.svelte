<script lang="ts">
	// Lifecycle buttons for the workspace detail header, gated by the
	// workspace grants, global permissions and the current status.
	import {
		Ban,
		FolderOpen,
		Play,
		RefreshCw,
		RotateCcw,
		Square,
		SquareTerminal,
		Trash2
	} from '@lucide/svelte';
	import { api } from '#lib/api/client.ts';
	import type { Workspace, WorkspaceDetail } from '#lib/api/types.ts';
	import { Button } from '#lib/components/ui/index.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { canDelete, canStart, toastError } from './workspace.ts';

	let {
		ws,
		onupdate,
		ondeleting
	}: {
		ws: WorkspaceDetail;
		onupdate: (w: Workspace) => void;
		ondeleting: (w: Workspace) => void;
	} = $props();

	type Action = 'start' | 'stop' | 'restart' | 'retry' | 'suspend' | 'delete';
	let running = $state<Action | null>(null);

	const g = $derived(ws.grants);
	const idle = $derived(ws.pending_job === null && running === null);
	const isAdmin = $derived(auth.can('workspaces.manage_all'));
	const isOwner = $derived(ws.owner_id === auth.me?.id);

	const show = $derived({
		start: g.Operate && canStart(ws.status) && (ws.status !== 'suspended' || isAdmin),
		stop: g.Operate && ws.status === 'running',
		restart: g.Operate && ws.status === 'running',
		retry: g.Manage && ws.status === 'failed',
		suspend: isAdmin && ws.status === 'running',
		delete:
			ws.status !== 'deleting' &&
			((isOwner && auth.can('workspaces.delete')) || (g.Manage && isAdmin)),
		terminal: g.Edit && ws.status === 'running' && auth.can('terminals.open'),
		files: g.Edit && ['ready', 'running', 'stopped'].includes(ws.status)
	});

	const messages: Record<Action, string> = {
		start: 'Starting workspace',
		stop: 'Stopping workspace',
		restart: 'Restarting workspace',
		retry: 'Provisioning retried',
		suspend: 'Workspace suspended',
		delete: 'Deleting workspace'
	};

	async function run(action: Action) {
		let reason = '';
		if (action === 'stop' || action === 'restart') {
			const ok = await confirmDialog.confirm({
				title: action === 'stop' ? `Stop ${ws.name}?` : `Restart ${ws.name}?`,
				message:
					action === 'stop'
						? 'All processes and terminal sessions in this workspace will be stopped. Files in the workspace volume are kept.'
						: 'All processes will be restarted and open terminal sessions will be disconnected.',
				confirmLabel: action === 'stop' ? 'Stop workspace' : 'Restart workspace',
				danger: true
			});
			if (!ok) return;
		} else if (action === 'suspend') {
			const r = await confirmDialog.ask({
				title: `Suspend ${ws.name}?`,
				message:
					'The workspace is stopped and its owner cannot start it again until an administrator does. The owner is notified with your reason.',
				reasonLabel: 'Reason (shown to the owner)',
				reasonRequired: true,
				confirmLabel: 'Suspend workspace',
				danger: true
			});
			if (!r.confirmed) return;
			reason = r.reason;
		} else if (action === 'delete') {
			const r = await confirmDialog.ask({
				title: `Delete ${ws.name}?`,
				message:
					'The workspace, all its containers and its persistent volume (all files) will be permanently deleted. This cannot be undone.',
				requireText: ws.slug,
				confirmLabel: 'Delete workspace',
				danger: true
			});
			if (!r.confirmed) return;
		}
		running = action;
		try {
			let updated: Workspace;
			if (action === 'delete') {
				updated = await api.del<Workspace>(`/workspaces/${ws.id}`);
				toast.success(messages[action], `${ws.name} is being deleted.`);
				ondeleting(updated);
			} else {
				updated = await api.post<Workspace>(
					`/workspaces/${ws.id}/${action}`,
					reason ? { reason } : undefined
				);
				toast.success(messages[action], ws.name);
				onupdate(updated);
			}
		} catch (e) {
			toastError(`Could not ${action} ${ws.name}`, e);
		} finally {
			running = null;
		}
	}
</script>

<div class="flex flex-wrap items-center gap-2">
	{#if show.terminal}
		<Button href="/terminal?workspace={ws.id}"
			><SquareTerminal class="size-4" /> Open terminal</Button
		>
	{/if}
	{#if show.files}
		<Button href="/workspaces/{ws.id}/files"><FolderOpen class="size-4" /> Files</Button>
	{/if}
	{#if show.start}
		<Button
			variant="primary"
			disabled={!idle}
			loading={running === 'start'}
			onclick={() => run('start')}><Play class="size-4" /> Start</Button
		>
	{/if}
	{#if show.stop}
		<Button disabled={!idle} loading={running === 'stop'} onclick={() => run('stop')}
			><Square class="size-4" /> Stop</Button
		>
	{/if}
	{#if show.restart}
		<Button disabled={!idle} loading={running === 'restart'} onclick={() => run('restart')}
			><RefreshCw class="size-4" /> Restart</Button
		>
	{/if}
	{#if show.retry}
		<Button disabled={!idle} loading={running === 'retry'} onclick={() => run('retry')}
			><RotateCcw class="size-4" /> Retry</Button
		>
	{/if}
	{#if show.suspend}
		<Button disabled={!idle} loading={running === 'suspend'} onclick={() => run('suspend')}
			><Ban class="size-4" /> Suspend</Button
		>
	{/if}
	{#if show.delete}
		<Button
			variant="danger"
			disabled={!idle || !canDelete(ws.status)}
			loading={running === 'delete'}
			title={canDelete(ws.status)
				? undefined
				: `A ${ws.status} workspace cannot be deleted. Stop it first.`}
			onclick={() => run('delete')}><Trash2 class="size-4" /> Delete</Button
		>
	{/if}
</div>
{#if show.delete && !canDelete(ws.status)}
	<p class="mt-1 text-right text-xs text-fg-muted">
		{ws.status === 'running'
			? 'To delete this workspace, stop it first.'
			: 'Deletion is possible once provisioning has finished.'}
	</p>
{/if}
{#if ws.pending_job}
	<p class="mt-1 text-right text-xs text-fg-muted" role="status">
		Waiting for “{ws.pending_job}” to finish…
	</p>
{/if}
