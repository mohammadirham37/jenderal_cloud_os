<script lang="ts">
	// Members tab: list, add (user lookup) and remove workspace members.
	import { Trash2, UserPlus, Users } from '@lucide/svelte';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import type { WorkspaceDetail, WorkspaceMember } from '#lib/api/types.ts';
	import type { UserLookup } from '#lib/api/types-workspaces.ts';
	import { Badge, Button, Card, EmptyState, Field, Spinner } from '#lib/components/ui/index.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { toastError } from './workspace.ts';

	let { ws, onchange }: { ws: WorkspaceDetail; onchange: () => void } = $props();

	const manage = $derived(ws.grants.Manage);

	let query = $state('');
	let results = $state<UserLookup[]>([]);
	let searching = $state(false);
	let searchError = $state('');
	let selected = $state<UserLookup | null>(null);
	let role = $state<'viewer' | 'editor'>('viewer');
	let errors = $state<Record<string, string>>({});
	let adding = $state(false);
	let removing = $state<string | null>(null);
	let timer: ReturnType<typeof setTimeout> | undefined;
	let seq = 0;

	function onsearch() {
		selected = null;
		searchError = '';
		clearTimeout(timer);
		const q = query.trim();
		if (q.length < 2) {
			results = [];
			return;
		}
		timer = setTimeout(async () => {
			const my = ++seq;
			searching = true;
			try {
				const r = await api.get<UserLookup[]>('/users/lookup', { q });
				if (my === seq) results = r.filter((u) => u.id !== ws.owner_id);
			} catch (e) {
				if (my === seq) {
					results = [];
					searchError =
						e instanceof ApiError && e.status === 403
							? 'You are not allowed to search users.'
							: errorMessage(e);
				}
			} finally {
				if (my === seq) searching = false;
			}
		}, 300);
	}

	function choose(u: UserLookup) {
		selected = u;
		query = u.username;
		results = [];
	}

	async function add(e: SubmitEvent) {
		e.preventDefault();
		errors = {};
		if (!selected) {
			errors = { user_id: 'Search for a user and select them from the list.' };
			return;
		}
		adding = true;
		try {
			await api.post<WorkspaceMember[]>(`/workspaces/${ws.id}/members`, {
				user_id: selected.id,
				role
			});
			toast.success('Member added', `${selected.username} is now a ${role}.`);
			selected = null;
			query = '';
			onchange();
		} catch (err) {
			if (err instanceof ApiError && err.status === 422 && Object.keys(err.details).length) {
				errors = { ...err.details };
			} else {
				toastError('Could not add member', err);
			}
		} finally {
			adding = false;
		}
	}

	async function remove(m: WorkspaceMember) {
		const ok = await confirmDialog.confirm({
			title: `Remove ${m.username}?`,
			message: `${m.full_name || m.username} will lose access to this workspace. Their open terminal sessions are closed.`,
			confirmLabel: 'Remove member',
			danger: true
		});
		if (!ok) return;
		removing = m.user_id;
		try {
			await api.del(`/workspaces/${ws.id}/members/${m.user_id}`);
			toast.success('Member removed', m.username);
			onchange();
		} catch (e) {
			toastError('Could not remove member', e);
		} finally {
			removing = null;
		}
	}
</script>

<div class="grid gap-6 {manage ? 'lg:grid-cols-3' : ''}">
	<Card
		title="Members"
		description="Viewers can see the workspace; editors can also use its terminal, files and apps."
		padded={false}
		class={manage ? 'lg:col-span-2' : ''}
	>
		<ul class="divide-y divide-border">
			<li class="flex items-center justify-between gap-3 px-5 py-3">
				<span class="text-sm font-medium">{ws.owner_username}</span>
				<Badge tone="accent">owner</Badge>
			</li>
			{#each ws.members as m (m.user_id)}
				<li class="flex items-center justify-between gap-3 px-5 py-3">
					<div class="min-w-0">
						<p class="truncate text-sm font-medium">{m.username}</p>
						{#if m.full_name}<p class="truncate text-xs text-fg-muted">{m.full_name}</p>{/if}
					</div>
					<div class="flex items-center gap-2">
						<Badge tone={m.role === 'editor' ? 'info' : 'neutral'}>{m.role}</Badge>
						{#if manage}
							<Button
								size="sm"
								variant="ghost"
								loading={removing === m.user_id}
								onclick={() => remove(m)}
								aria-label="Remove {m.username}"><Trash2 class="size-3.5" /></Button
							>
						{/if}
					</div>
				</li>
			{/each}
		</ul>
		{#if ws.members.length === 0}
			<EmptyState
				icon={Users}
				title="No additional members"
				description={manage
					? 'Add people to share this workspace with them.'
					: 'Only the owner (and project members, if any) can access this workspace.'}
			/>
		{/if}
	</Card>

	{#if manage}
		<Card title="Add member">
			<form class="space-y-4" novalidate onsubmit={add}>
				<Field
					label="User"
					id="member-user"
					error={errors.user_id ?? searchError}
					hint="Type at least 2 characters of a username or name."
				>
					<div class="relative">
						<input
							id="member-user"
							class="input"
							bind:value={query}
							oninput={onsearch}
							autocomplete="off"
							role="combobox"
							aria-expanded={results.length > 0}
							aria-controls="member-results"
							aria-autocomplete="list"
						/>
						{#if searching}<span class="absolute top-1/2 right-2.5 -translate-y-1/2 text-fg-subtle"
								><Spinner size={14} /></span
							>{/if}
					</div>
					{#if results.length}
						<ul
							id="member-results"
							class="mt-1 max-h-56 overflow-y-auto rounded-md border border-border bg-panel shadow-sm"
							role="listbox"
						>
							{#each results as u (u.id)}
								<li role="option" aria-selected={selected?.id === u.id}>
									<button
										type="button"
										class="w-full px-3 py-2 text-left text-sm hover:bg-panel-2 focus-visible:bg-panel-2"
										onclick={() => choose(u)}
									>
										<span class="font-medium">{u.username}</span>
										{#if u.full_name}<span class="text-fg-muted"> · {u.full_name}</span>{/if}
									</button>
								</li>
							{/each}
						</ul>
					{:else if query.trim().length >= 2 && !searching && !selected && !searchError}
						<p class="mt-1 text-xs text-fg-muted">No matching active users.</p>
					{/if}
				</Field>
				<Field label="Role" id="member-role" error={errors.role}>
					<select id="member-role" class="input" bind:value={role}>
						<option value="viewer">Viewer — read-only</option>
						<option value="editor">Editor — terminal, files and apps</option>
					</select>
				</Field>
				<Button type="submit" variant="primary" loading={adding} disabled={!selected}
					><UserPlus class="size-4" /> Add member</Button
				>
			</form>
		</Card>
	{/if}
</div>
