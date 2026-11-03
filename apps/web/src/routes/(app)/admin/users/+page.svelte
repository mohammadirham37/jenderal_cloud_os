<script lang="ts">
	import { Plus, ShieldCheck, Users } from '@lucide/svelte';
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
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Quota, Role, User } from '#lib/api/types.ts';
	import type { CreateUserResponse } from '#lib/api/types-admin.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { dateTime, relative } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import SecretOnce from '#lib/components/admin/SecretOnce.svelte';
	import { guarded } from '#lib/components/admin/access.ts';
	import { handleFormError } from '#lib/components/admin/forms.ts';
	import { canGrant, myMaxRank } from '#lib/components/admin/roles.ts';

	const perPage = 20;
	let q = $state('');
	let search = $state('');
	let status = $state('');
	let role = $state(page.url.searchParams.get('role') ?? '');
	let sort = $state('username');
	let pageNo = $state(1);

	const allowed = $derived(auth.can('users.view'));
	// Query values are read synchronously, so changing a filter reloads.
	const users = resource(
		guarded(['users.view'], () =>
			api.list<User>('/users', { q: search, status, role, sort, page: pageNo, per_page: perPage })
		)
	);
	const roles = resource(guarded(['users.view'], () => api.get<Role[]>('/roles')));
	const quotas = resource(
		guarded(['quotas.view', 'quotas.manage'], () => api.get<Quota[]>('/quotas'))
	);

	const roleName = $derived(new Map((roles.data ?? []).map((r) => [r.key, r.name])));
	const maxRank = $derived(myMaxRank(roles.data ?? []));
	const grantable = $derived((roles.data ?? []).filter((r) => canGrant(r.rank, maxRank)));

	function resetPage() {
		pageNo = 1;
	}

	// Create user
	let createOpen = $state(false);
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let form = $state(blank());
	let tempPassword = $state<string | null>(null);
	let createdUser = $state<User | null>(null);

	function blank() {
		return {
			username: '',
			email: '',
			full_name: '',
			identity_number: '',
			roles: [] as string[],
			quota_key: '',
			password: ''
		};
	}
	function openCreate() {
		form = blank();
		errors = {};
		tempPassword = null;
		createdUser = null;
		createOpen = true;
	}
	function closeCreate() {
		createOpen = false;
		tempPassword = null;
		form.password = '';
	}

	async function create(e: SubmitEvent) {
		e.preventDefault();
		errors = {};
		if (form.roles.length === 0) {
			errors = { roles: 'Select at least one role.' };
			return;
		}
		saving = true;
		try {
			const res = await api.post<CreateUserResponse>('/users', {
				username: form.username.trim(),
				email: form.email.trim(),
				full_name: form.full_name.trim(),
				identity_number: form.identity_number.trim() || null,
				roles: form.roles,
				quota_key: form.quota_key || null,
				password: form.password || null
			});
			form.password = '';
			toast.success('User created', res.user.username);
			users.reload();
			if (res.temporary_password) {
				createdUser = res.user;
				tempPassword = res.temporary_password;
			} else {
				createOpen = false;
			}
		} catch (err) {
			errors = handleFormError(err, 'Could not create the user');
		} finally {
			saving = false;
		}
	}

	const columns = [
		{ key: 'username', label: 'User', sortable: true },
		{ key: 'email', label: 'Email', sortable: true },
		{ key: 'roles', label: 'Roles' },
		{ key: 'quota', label: 'Quota' },
		{ key: 'status', label: 'Status' },
		{ key: 'last_login_at', label: 'Last login', sortable: true },
		{ key: 'created_at', label: 'Created', sortable: true }
	];
</script>

<PageHeader title="Users" description="Accounts, roles, quota packages and account status.">
	{#snippet actions()}
		{#if allowed && auth.can('users.create')}
			<Button variant="primary" onclick={openCreate}><Plus class="size-4" /> New user</Button>
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else}
	<Card padded={false}>
		<div class="flex flex-wrap items-end gap-3 border-b border-border px-5 py-3">
			<div class="w-full sm:w-72">
				<SearchInput
					bind:value={q}
					placeholder="Search username, name, email, NIM/NIP"
					onsearch={(v) => {
						search = v.trim();
						resetPage();
					}}
				/>
			</div>
			<div>
				<label for="f-status" class="sr-only">Status</label>
				<select id="f-status" class="input w-auto" bind:value={status} onchange={resetPage}>
					<option value="">All statuses</option>
					<option value="active">Active</option>
					<option value="suspended">Suspended</option>
				</select>
			</div>
			<div>
				<label for="f-role" class="sr-only">Role</label>
				<select id="f-role" class="input w-auto" bind:value={role} onchange={resetPage}>
					<option value="">All roles</option>
					{#each roles.data ?? [] as r (r.id)}<option value={r.key}>{r.name}</option>{/each}
				</select>
			</div>
		</div>
		<DataTable
			{columns}
			rows={users.data?.data ?? []}
			loading={users.loading}
			error={users.error}
			retry={users.reload}
			bind:sort
			onsort={resetPage}
		>
			{#snippet empty()}
				{#if search || status || role}
					<EmptyState
						icon={Users}
						title="No users match the filters"
						description="Try a different search term or clear the filters."
					>
						{#snippet action()}<Button
								onclick={() => {
									q = '';
									search = '';
									status = '';
									role = '';
									resetPage();
								}}>Clear filters</Button
							>{/snippet}
					</EmptyState>
				{:else}
					<EmptyState
						icon={Users}
						title="No users yet"
						description="Create accounts for students and lecturers."
					>
						{#snippet action()}{#if auth.can('users.create')}<Button
									variant="primary"
									onclick={openCreate}><Plus class="size-4" /> New user</Button
								>{/if}{/snippet}
					</EmptyState>
				{/if}
			{/snippet}
			{#snippet row(u)}
				<tr class="hover:bg-panel-2/40">
					<td class="px-4 py-3">
						<a href="/admin/users/{u.id}" class="font-medium hover:underline">{u.username}</a>
						<p class="text-xs text-fg-muted">
							{u.full_name}{#if u.identity_number}
								· {u.identity_number}{/if}
						</p>
					</td>
					<td class="px-4 py-3 text-fg-muted">{u.email}</td>
					<td class="px-4 py-3">
						<div class="flex flex-wrap gap-1">
							{#each u.roles as r (r)}<Badge tone="accent">{roleName.get(r) ?? r}</Badge>{/each}
						</div>
					</td>
					<td class="px-4 py-3 text-fg-muted">{u.quota_key ?? 'Default'}</td>
					<td class="px-4 py-3">
						<div class="flex flex-wrap items-center gap-1">
							<StatusBadge status={u.status} />
							{#if u.mfa_enabled}<span title="MFA enabled"
									><ShieldCheck class="size-4 text-success" aria-label="MFA enabled" /></span
								>{/if}
							{#if u.locked_until && new Date(u.locked_until) > new Date()}<Badge tone="warning"
									>Locked</Badge
								>{/if}
						</div>
					</td>
					<td class="px-4 py-3 text-xs whitespace-nowrap" title={dateTime(u.last_login_at)}
						>{u.last_login_at ? relative(u.last_login_at) : 'Never'}</td
					>
					<td class="px-4 py-3 text-xs whitespace-nowrap text-fg-muted">{dateTime(u.created_at)}</td
					>
				</tr>
			{/snippet}
		</DataTable>
		{#if users.data && users.data.meta.total > 0}
			<div class="border-t border-border px-4">
				<Pagination bind:page={pageNo} {perPage} total={users.data.meta.total} />
			</div>
		{/if}
	</Card>
{/if}

<Modal
	bind:open={createOpen}
	title={tempPassword ? 'User created' : 'New user'}
	description={tempPassword ? undefined : 'The user must change the password at first login.'}
	size="lg"
	onclose={closeCreate}
>
	{#if tempPassword && createdUser}
		<p class="mb-4 text-sm text-fg-muted">
			Account <strong class="text-fg">{createdUser.username}</strong> was created with a generated temporary
			password. Give it to the user through a secure channel; they must change it at first login.
		</p>
		<SecretOnce
			label="Temporary password"
			value={tempPassword}
			warning="This password is shown only once and is not stored in readable form. Copy it now."
		/>
	{:else}
		<form id="create-user" class="grid gap-4 sm:grid-cols-2" onsubmit={create}>
			<Field
				label="Username"
				id="cu-username"
				required
				error={errors.username}
				hint="3-32 lowercase letters, digits, '.', '_' or '-'"
			>
				<input
					id="cu-username"
					class="input"
					bind:value={form.username}
					autocomplete="off"
					required
					aria-invalid={!!errors.username}
				/>
			</Field>
			<Field label="Email" id="cu-email" required error={errors.email}>
				<input
					id="cu-email"
					type="email"
					class="input"
					bind:value={form.email}
					autocomplete="off"
					required
					aria-invalid={!!errors.email}
				/>
			</Field>
			<Field label="Full name" id="cu-name" required error={errors.full_name}>
				<input
					id="cu-name"
					class="input"
					bind:value={form.full_name}
					required
					aria-invalid={!!errors.full_name}
				/>
			</Field>
			<Field
				label="Identity number (NIM/NIP)"
				id="cu-idn"
				error={errors.identity_number}
				hint="Optional"
			>
				<input id="cu-idn" class="input" bind:value={form.identity_number} maxlength="50" />
			</Field>
			<div class="sm:col-span-2">
				<fieldset aria-describedby="cu-roles-help">
					<legend class="label">Roles <span class="text-danger" aria-hidden="true">*</span></legend>
					{#if roles.error}
						<p class="text-sm text-danger">Roles could not be loaded.</p>
					{:else if grantable.length === 0}
						<p class="text-sm text-fg-muted">
							You cannot grant any role. Ask an administrator with a higher role.
						</p>
					{:else}
						<div class="grid gap-2 sm:grid-cols-2">
							{#each grantable as r (r.id)}
								<label
									class="flex items-start gap-2 rounded-md border border-border p-2.5 text-sm hover:bg-panel-2"
								>
									<input type="checkbox" class="mt-0.5" value={r.key} bind:group={form.roles} />
									<span>
										<span class="font-medium">{r.name}</span>
										{#if r.requires_mfa}<Badge tone="warning">MFA</Badge>{/if}
										<span class="block text-xs text-fg-muted">{r.description || r.key}</span>
									</span>
								</label>
							{/each}
						</div>
					{/if}
					<p id="cu-roles-help" class="mt-1 text-xs text-fg-muted">
						Only roles below your own rank{auth.can('roles.manage') ? ' (or equal)' : ''} can be granted.
					</p>
					{#if errors.roles}<p class="mt-1 text-xs text-danger" role="alert">{errors.roles}</p>{/if}
				</fieldset>
			</div>
			<Field
				label="Quota package"
				id="cu-quota"
				error={errors.quota_key}
				hint={quotas.error ? 'Quota packages could not be loaded; the default applies.' : undefined}
			>
				<select id="cu-quota" class="input" bind:value={form.quota_key} disabled={!quotas.data}>
					<option value="">Platform default</option>
					{#each quotas.data ?? [] as qp (qp.id)}<option value={qp.key}>{qp.name} ({qp.key})</option
						>{/each}
				</select>
			</Field>
			<Field
				label="Initial password"
				id="cu-password"
				error={errors.password}
				hint="Optional. Leave empty to generate a temporary password."
			>
				<input
					id="cu-password"
					type="password"
					class="input"
					bind:value={form.password}
					autocomplete="new-password"
					aria-invalid={!!errors.password}
				/>
			</Field>
		</form>
	{/if}
	{#snippet footer()}
		{#if tempPassword}
			{#if createdUser}<Button href="/admin/users/{createdUser.id}">Open user</Button>{/if}
			<Button variant="primary" onclick={closeCreate}>I have copied the password</Button>
		{:else}
			<Button onclick={closeCreate}>Cancel</Button>
			<Button
				variant="primary"
				type="submit"
				form="create-user"
				loading={saving}
				disabled={grantable.length === 0}>Create user</Button
			>
		{/if}
	{/snippet}
</Modal>
