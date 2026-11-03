<script lang="ts">
	import { Check, Pencil, Plus, Shield, Trash2 } from '@lucide/svelte';
	import {
		Alert,
		Badge,
		Button,
		Card,
		DataTable,
		ErrorState,
		Field,
		Modal,
		PageHeader,
		Skeleton,
		Switch
	} from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Permission, Role } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { titleCase } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import { guarded } from '#lib/components/admin/access.ts';
	import { handleFormError } from '#lib/components/admin/forms.ts';
	import { groupBy } from '#lib/components/admin/group.ts';
	import { myMaxRank } from '#lib/components/admin/roles.ts';

	const perms = ['roles.view', 'roles.manage'];
	const allowed = $derived(auth.can(...perms));
	const canManage = $derived(auth.can('roles.manage'));
	const roles = resource(guarded(perms, () => api.get<Role[]>('/roles')));
	const permissions = resource(guarded(perms, () => api.get<Permission[]>('/permissions')));

	const maxRank = $derived(myMaxRank(roles.data ?? []));
	const grouped = $derived(groupBy(permissions.data ?? [], (p) => p.category));
	const editable = (r: Role) => canManage && r.key !== 'super_admin' && r.rank < maxRank;

	// Matrix filter
	let matrixFilter = $state('');
	const filteredGroups = $derived.by(() => {
		const f = matrixFilter.trim().toLowerCase();
		if (!f) return grouped;
		return grouped
			.map(
				([c, list]) =>
					[
						c,
						list.filter(
							(p) =>
								p.key.toLowerCase().includes(f) ||
								p.description.toLowerCase().includes(f) ||
								c.toLowerCase().includes(f)
						)
					] as [string, Permission[]]
			)
			.filter(([, list]) => list.length > 0);
	});

	// Create / edit modal
	let open = $state(false);
	let editing = $state<Role | null>(null);
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let form = $state({
		key: '',
		name: '',
		description: '',
		rank: 10,
		requires_mfa: false,
		permissions: [] as string[]
	});

	function openCreate() {
		editing = null;
		errors = {};
		form = {
			key: '',
			name: '',
			description: '',
			rank: Math.max(1, Math.min(10, maxRank - 1)),
			requires_mfa: false,
			permissions: []
		};
		open = true;
	}
	function openEdit(r: Role) {
		editing = r;
		errors = {};
		form = {
			key: r.key,
			name: r.name,
			description: r.description,
			rank: r.rank,
			requires_mfa: r.requires_mfa,
			permissions: [...r.permissions]
		};
		open = true;
	}
	function toggleCategory(list: Permission[], on: boolean) {
		const keys = list.map((p) => p.key);
		form.permissions = on
			? [...new Set([...form.permissions, ...keys])]
			: form.permissions.filter((k) => !keys.includes(k));
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		errors = {};
		const rank = Number(form.rank);
		if (!Number.isInteger(rank) || rank < 1 || rank >= maxRank) {
			errors = { rank: `Must be between 1 and ${maxRank - 1}.` };
			return;
		}
		saving = true;
		const body = {
			name: form.name.trim(),
			description: form.description.trim(),
			rank,
			requires_mfa: form.requires_mfa,
			permissions: form.permissions
		};
		try {
			if (editing) {
				await api.patch<Role>(`/roles/${editing.id}`, body);
				toast.success('Role updated', body.name);
			} else {
				await api.post<Role>('/roles', { ...body, key: form.key.trim() });
				toast.success('Role created', body.name);
			}
			open = false;
			roles.reload();
		} catch (err) {
			errors = handleFormError(
				err,
				editing ? 'Could not update the role' : 'Could not create the role'
			);
		} finally {
			saving = false;
		}
	}

	async function remove(r: Role) {
		const ok = await confirmDialog.confirm({
			title: `Delete role “${r.name}”?`,
			message:
				r.user_count > 0
					? `${r.user_count} user(s) still have this role. Remove it from them first; the server will refuse otherwise.`
					: 'This custom role will be removed permanently.',
			danger: true,
			confirmLabel: 'Delete role'
		});
		if (!ok) return;
		try {
			await api.del(`/roles/${r.id}`);
			toast.success('Role deleted', r.name);
			roles.reload();
		} catch (err) {
			toast.error('Could not delete the role', errorMessage(err));
		}
	}

	const columns = [
		{ key: 'name', label: 'Role' },
		{ key: 'rank', label: 'Rank' },
		{ key: 'mfa', label: 'MFA' },
		{ key: 'users', label: 'Users' },
		{ key: 'perms', label: 'Permissions' },
		{ key: 'actions', label: 'Actions', class: 'text-right' }
	];
</script>

<PageHeader
	title="Roles & Permissions"
	description="Roles bundle permissions. Higher ranks can manage users with lower ranks."
>
	{#snippet actions()}
		{#if allowed && canManage}
			<Button variant="primary" onclick={openCreate} disabled={!roles.data || maxRank <= 1}
				><Plus class="size-4" /> New role</Button
			>
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else}
	{#if !canManage}
		<div class="mb-6">
			<Alert tone="info" title="Read-only"
				>You can view roles but not change them (requires roles.manage).</Alert
			>
		</div>
	{/if}
	<Card title="Roles" padded={false}>
		<DataTable
			{columns}
			rows={roles.data ?? []}
			loading={roles.loading}
			error={roles.error}
			retry={roles.reload}
			emptyTitle="No roles defined"
		>
			{#snippet row(r)}
				<tr>
					<td class="px-4 py-3">
						<p class="font-medium">
							{r.name}
							{#if r.is_system}<Badge>Built-in</Badge>{/if}
						</p>
						<p class="font-mono text-xs text-fg-muted">{r.key}</p>
						{#if r.description}<p class="text-xs text-fg-muted">{r.description}</p>{/if}
					</td>
					<td class="px-4 py-3 tabular-nums">{r.rank}</td>
					<td class="px-4 py-3"
						>{#if r.requires_mfa}<Badge tone="warning">Required</Badge>{:else}<span
								class="text-fg-muted">—</span
							>{/if}</td
					>
					<td class="px-4 py-3 tabular-nums">
						<a href="/admin/users?role={r.key}" class="link">{r.user_count}</a>
					</td>
					<td class="px-4 py-3 tabular-nums">{r.permissions.length}</td>
					<td class="px-4 py-3 text-right whitespace-nowrap">
						{#if editable(r)}
							<Button
								size="sm"
								variant="ghost"
								onclick={() => openEdit(r)}
								aria-label="Edit {r.name}"><Pencil class="size-3.5" /> Edit</Button
							>
							{#if !r.is_system}
								<Button
									size="sm"
									variant="ghost"
									onclick={() => remove(r)}
									aria-label="Delete {r.name}"><Trash2 class="size-3.5 text-danger" /></Button
								>
							{/if}
						{:else if canManage}
							<span class="text-xs text-fg-muted"
								>{r.key === 'super_admin' ? 'Protected' : 'At or above your rank'}</span
							>
						{/if}
					</td>
				</tr>
			{/snippet}
		</DataTable>
	</Card>

	<Card
		title="Permission matrix"
		description="Which role grants which permission."
		class="mt-6"
		padded={false}
	>
		{#snippet actions()}
			<input
				type="search"
				class="input h-8 w-56"
				placeholder="Filter permissions"
				aria-label="Filter permissions"
				bind:value={matrixFilter}
			/>
		{/snippet}
		{#if permissions.error}
			<ErrorState error={permissions.error} retry={permissions.reload} />
		{:else if !permissions.data || !roles.data}
			<div class="p-5"><Skeleton lines={8} /></div>
		{:else}
			<div class="max-h-[70vh] overflow-auto">
				<table class="w-full text-left text-sm">
					<thead class="sticky top-0 z-10 bg-panel text-xs text-fg-muted">
						<tr class="border-b border-border">
							<th scope="col" class="px-4 py-2.5 font-medium">Permission</th>
							{#each roles.data as r (r.id)}
								<th scope="col" class="px-2 py-2.5 text-center font-medium whitespace-nowrap"
									>{r.name}</th
								>
							{/each}
						</tr>
					</thead>
					<tbody>
						{#each filteredGroups as [category, list] (category)}
							<tr class="bg-panel-2/60">
								<th
									scope="colgroup"
									colspan={roles.data.length + 1}
									class="px-4 py-1.5 text-xs font-semibold tracking-wide text-fg-muted uppercase"
									>{titleCase(category)}</th
								>
							</tr>
							{#each list as p (p.key)}
								<tr class="border-b border-border hover:bg-panel-2/40">
									<th scope="row" class="px-4 py-2 font-normal">
										<span class="font-mono text-xs">{p.key}</span>
										<span class="block text-xs text-fg-muted">{p.description}</span>
									</th>
									{#each roles.data as r (r.id)}
										<td class="px-2 py-2 text-center">
											{#if r.permissions.includes(p.key)}
												<Check
													class="mx-auto size-4 text-success"
													aria-label="Granted to {r.name}"
												/>
											{:else}
												<span class="text-fg-subtle" aria-label="Not granted to {r.name}">·</span>
											{/if}
										</td>
									{/each}
								</tr>
							{/each}
						{:else}
							<tr
								><td colspan={roles.data.length + 1} class="px-4 py-8 text-center text-fg-muted"
									>No permissions match the filter.</td
								></tr
							>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</Card>
{/if}

<Modal
	bind:open
	title={editing ? `Edit role: ${editing.name}` : 'New role'}
	description={editing
		? 'Changes apply to every user with this role on their next request.'
		: 'Custom roles must rank below your own role.'}
	size="xl"
>
	<form id="role-form" class="space-y-5" onsubmit={submit}>
		<div class="grid gap-4 sm:grid-cols-2">
			{#if !editing}
				<Field
					label="Key"
					id="r-key"
					required
					error={errors.key}
					hint="Lowercase letters, digits and underscores; cannot be changed later."
				>
					<input id="r-key" class="input font-mono" bind:value={form.key} required />
				</Field>
			{/if}
			<Field label="Name" id="r-name" required error={errors.name}>
				<input id="r-name" class="input" bind:value={form.name} maxlength="80" required />
			</Field>
			<Field
				label="Rank"
				id="r-rank"
				required
				error={errors.rank}
				hint="1 to {Math.max(1, maxRank - 1)}. Higher ranks can manage lower ranks."
			>
				<input
					id="r-rank"
					type="number"
					class="input"
					min="1"
					max={maxRank - 1}
					step="1"
					bind:value={form.rank}
					required
				/>
			</Field>
			<Field label="Description" id="r-desc" error={errors.description}>
				<input id="r-desc" class="input" bind:value={form.description} />
			</Field>
			<div class="flex items-center gap-3 sm:col-span-2">
				<Switch id="r-mfa" label="Requires MFA" bind:checked={form.requires_mfa} />
				<label for="r-mfa" class="text-sm"
					>Require multi-factor authentication for users with this role</label
				>
			</div>
		</div>

		<fieldset>
			<legend class="label">Permissions ({form.permissions.length} selected)</legend>
			{#if errors.permissions}<p class="mb-2 text-xs text-danger" role="alert">
					{errors.permissions}
				</p>{/if}
			<div class="grid gap-4 md:grid-cols-2">
				{#each grouped as [category, list] (category)}
					{@const all = list.every((p) => form.permissions.includes(p.key))}
					<div class="rounded-md border border-border">
						<div
							class="flex items-center justify-between border-b border-border bg-panel-2/60 px-3 py-2"
						>
							<span class="text-xs font-semibold tracking-wide text-fg-muted uppercase"
								>{titleCase(category)}</span
							>
							<button type="button" class="link text-xs" onclick={() => toggleCategory(list, !all)}
								>{all ? 'Clear' : 'Select all'}</button
							>
						</div>
						<div class="space-y-1 p-2">
							{#each list as p (p.key)}
								<label class="flex items-start gap-2 rounded px-1 py-1 text-sm hover:bg-panel-2">
									<input
										type="checkbox"
										class="mt-0.5"
										value={p.key}
										bind:group={form.permissions}
									/>
									<span>
										<span class="font-mono text-xs">{p.key}</span>
										<span class="block text-xs text-fg-muted">{p.description}</span>
									</span>
								</label>
							{/each}
						</div>
					</div>
				{/each}
			</div>
		</fieldset>
	</form>
	{#snippet footer()}
		<Button onclick={() => (open = false)}>Cancel</Button>
		<Button type="submit" form="role-form" variant="primary" loading={saving}
			><Shield class="size-4" /> {editing ? 'Save role' : 'Create role'}</Button
		>
	{/snippet}
</Modal>
