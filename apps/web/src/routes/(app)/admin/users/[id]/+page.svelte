<script lang="ts">
	import { KeyRound, Play, ShieldOff, UserX } from '@lucide/svelte';
	import { page } from '$app/state';
	import {
		Alert,
		Badge,
		Button,
		Card,
		EmptyState,
		ErrorState,
		Field,
		Meter,
		Modal,
		PageHeader,
		Skeleton,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { AuditLog, Quota, Role, Usage, User } from '#lib/api/types.ts';
	import type { ResetPasswordResponse } from '#lib/api/types-admin.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { crumbs } from '#lib/stores/breadcrumb.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes, cores, dateTime, relative } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import SecretOnce from '#lib/components/admin/SecretOnce.svelte';
	import { guarded } from '#lib/components/admin/access.ts';
	import { handleFormError } from '#lib/components/admin/forms.ts';
	import { canGrant, myMaxRank } from '#lib/components/admin/roles.ts';

	const id = $derived(page.params.id ?? '');
	const allowed = $derived(auth.can('users.view'));
	const isSelf = $derived(auth.me?.id === id);

	const user = resource(guarded(['users.view'], () => api.get<User>(`/users/${id}`)));
	const roles = resource(guarded(['users.view'], () => api.get<Role[]>('/roles')));
	const quotas = resource(
		guarded(['quotas.view', 'quotas.manage'], () => api.get<Quota[]>('/quotas'))
	);
	const usage = resource(
		guarded(['users.view'], () => api.get<{ quota: Quota; usage: Usage }>(`/users/${id}/usage`))
	);
	const audit = resource(
		guarded(['audit.view'], () =>
			api.list<AuditLog>('/admin/audit-logs', { actor_id: id, per_page: 20 })
		)
	);

	$effect(() => {
		if (user.data) crumbs.set(user.data.id, user.data.username);
	});

	const maxRank = $derived(myMaxRank(roles.data ?? []));
	const roleByKey = $derived(new Map((roles.data ?? []).map((r) => [r.key, r])));
	const targetRank = $derived(
		Math.max(0, ...(user.data?.roles ?? []).map((k) => roleByKey.get(k)?.rank ?? 0))
	);
	// Mirrors users.canManage: strictly lower rank, or equal with roles.manage.
	const canManageTarget = $derived(
		!isSelf && (auth.can('roles.manage') ? targetRank <= maxRank : targetRank < maxRank)
	);
	const canEditRoles = $derived(
		!isSelf && auth.can('users.assign_roles', 'roles.manage') && canManageTarget
	);
	const canEdit = $derived(auth.can('users.update') && (isSelf || canManageTarget));

	// Profile form
	let form = $state({
		email: '',
		full_name: '',
		identity_number: '',
		quota_key: '',
		roles: [] as string[]
	});
	let loadedFor = $state('');
	let errors = $state<Record<string, string>>({});
	let saving = $state(false);
	$effect(() => {
		const u = user.data;
		if (u && loadedFor !== `${u.id}:${u.updated_at}`) {
			loadedFor = `${u.id}:${u.updated_at}`;
			form = {
				email: u.email,
				full_name: u.full_name,
				identity_number: u.identity_number ?? '',
				quota_key: u.quota_key ?? '',
				roles: [...u.roles]
			};
		}
	});
	const sameRoles = (a: string[], b: string[]) =>
		a.length === b.length && [...a].sort().join() === [...b].sort().join();
	const dirty = $derived.by(() => {
		const u = user.data;
		if (!u) return false;
		return (
			form.email.trim() !== u.email ||
			form.full_name.trim() !== u.full_name ||
			form.identity_number.trim() !== (u.identity_number ?? '') ||
			form.quota_key !== (u.quota_key ?? '') ||
			!sameRoles(form.roles, u.roles)
		);
	});

	async function save(e: SubmitEvent) {
		e.preventDefault();
		const u = user.data;
		if (!u) return;
		errors = {};
		const body: Record<string, unknown> = {};
		if (form.email.trim() !== u.email) body.email = form.email.trim();
		if (form.full_name.trim() !== u.full_name) body.full_name = form.full_name.trim();
		if (form.identity_number.trim() !== (u.identity_number ?? ''))
			body.identity_number = form.identity_number.trim();
		if (form.quota_key !== (u.quota_key ?? '')) body.quota_key = form.quota_key;
		if (!sameRoles(form.roles, u.roles)) {
			if (form.roles.length === 0) {
				errors = { roles: 'At least one role is required.' };
				return;
			}
			const ok = await confirmDialog.confirm({
				title: 'Change roles?',
				message: `Changing roles signs ${u.username} out of all sessions so the new permissions take effect.`,
				confirmLabel: 'Save and sign out user'
			});
			if (!ok) return;
			body.roles = form.roles;
		}
		saving = true;
		try {
			const updated = await api.patch<User>(`/users/${u.id}`, body);
			user.set(updated);
			usage.reload();
			toast.success('User updated', updated.username);
		} catch (err) {
			errors = handleFormError(err, 'Could not update the user');
		} finally {
			saving = false;
		}
	}

	// Status & security actions
	let busy = $state<string | null>(null);
	let resetResult = $state<ResetPasswordResponse | null>(null);
	let resetOpen = $state(false);

	async function suspend() {
		const u = user.data;
		if (!u) return;
		const { confirmed, reason } = await confirmDialog.ask({
			title: `Suspend ${u.username}?`,
			message:
				'The user is signed out immediately, cannot log in and their running workspaces are stopped. Data is kept.',
			reasonLabel: 'Reason (shown to administrators and recorded in the audit log)',
			reasonRequired: true,
			danger: true,
			confirmLabel: 'Suspend user'
		});
		if (!confirmed) return;
		busy = 'suspend';
		try {
			user.set(await api.post<User>(`/users/${u.id}/suspend`, { reason }));
			toast.success('User suspended', u.username);
			audit.reload();
		} catch (err) {
			toast.error('Could not suspend the user', errorMessage(err));
		} finally {
			busy = null;
		}
	}

	async function activate() {
		const u = user.data;
		if (!u) return;
		const ok = await confirmDialog.confirm({
			title: `Activate ${u.username}?`,
			message: 'The user will be able to log in again. Failed-login lockouts are cleared.',
			confirmLabel: 'Activate user'
		});
		if (!ok) return;
		busy = 'activate';
		try {
			user.set(await api.post<User>(`/users/${u.id}/activate`, {}));
			toast.success('User activated', u.username);
			audit.reload();
		} catch (err) {
			toast.error('Could not activate the user', errorMessage(err));
		} finally {
			busy = null;
		}
	}

	async function resetPassword() {
		const u = user.data;
		if (!u) return;
		const ok = await confirmDialog.confirm({
			title: `Reset password of ${u.username}?`,
			message:
				'A single-use reset link is created and all of the user’s sessions are revoked. The current password keeps working until the link is used.',
			danger: true,
			confirmLabel: 'Create reset link'
		});
		if (!ok) return;
		busy = 'reset';
		try {
			resetResult = await api.post<ResetPasswordResponse>(`/users/${u.id}/reset-password`);
			resetOpen = true;
			audit.reload();
		} catch (err) {
			toast.error('Could not reset the password', errorMessage(err));
		} finally {
			busy = null;
		}
	}

	async function resetMfa() {
		const u = user.data;
		if (!u) return;
		const { confirmed, reason } = await confirmDialog.ask({
			title: `Reset MFA of ${u.username}?`,
			message:
				'The user’s authenticator is removed and all sessions are revoked. They must enroll MFA again at next login if their role requires it.',
			reasonLabel: 'Reason (for example: lost phone, identity verified in person)',
			reasonRequired: true,
			danger: true,
			confirmLabel: 'Reset MFA'
		});
		if (!confirmed) return;
		busy = 'mfa';
		try {
			await api.post(`/users/${u.id}/reset-mfa`, { reason });
			toast.success('MFA reset', u.username);
			user.reload();
			audit.reload();
		} catch (err) {
			toast.error('Could not reset MFA', errorMessage(err));
		} finally {
			busy = null;
		}
	}

	function closeReset() {
		resetOpen = false;
		resetResult = null;
	}
</script>

<PageHeader
	title={user.data ? user.data.full_name || user.data.username : 'User'}
	description={user.data ? `@${user.data.username} · ${user.data.email}` : undefined}
>
	{#snippet meta()}
		{#if user.data}
			<StatusBadge status={user.data.status} />
			{#if user.data.mfa_enabled}<Badge tone="success">MFA enabled</Badge>{:else}<Badge
					>No MFA</Badge
				>{/if}
			{#if user.data.must_change_password}<Badge tone="warning">Must change password</Badge>{/if}
			{#if isSelf}<Badge tone="info">This is you</Badge>{/if}
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else if user.error}
	<Card><ErrorState error={user.error} retry={user.reload} /></Card>
{:else if !user.data}
	<div class="grid gap-6 lg:grid-cols-3">
		<div class="card p-5 lg:col-span-2"><Skeleton lines={8} /></div>
		<div class="card p-5"><Skeleton lines={5} /></div>
	</div>
{:else}
	{@const u = user.data}
	{#if u.status === 'suspended'}
		<div class="mb-6">
			<Alert tone="warning" title="This account is suspended">
				Reason: {u.suspended_reason || 'not recorded'}
			</Alert>
		</div>
	{/if}
	<div class="grid gap-6 lg:grid-cols-3">
		<Card
			title="Profile"
			class="lg:col-span-2"
			description={canEdit ? undefined : 'You cannot edit this user (equal or higher role).'}
		>
			<form class="grid gap-4 sm:grid-cols-2" onsubmit={save}>
				<Field label="Email" id="u-email" error={errors.email}>
					<input
						id="u-email"
						type="email"
						class="input"
						bind:value={form.email}
						disabled={!canEdit}
					/>
				</Field>
				<Field label="Full name" id="u-name" error={errors.full_name}>
					<input id="u-name" class="input" bind:value={form.full_name} disabled={!canEdit} />
				</Field>
				<Field label="Identity number (NIM/NIP)" id="u-idn" error={errors.identity_number}>
					<input
						id="u-idn"
						class="input"
						bind:value={form.identity_number}
						maxlength="50"
						disabled={!canEdit}
					/>
				</Field>
				<Field
					label="Quota package"
					id="u-quota"
					error={errors.quota_key}
					hint={quotas.data ? undefined : 'Quota packages are not visible to you.'}
				>
					<select
						id="u-quota"
						class="input"
						bind:value={form.quota_key}
						disabled={!canEdit || !quotas.data}
					>
						<option value="">Platform default</option>
						{#if form.quota_key && !quotas.data}<option value={form.quota_key}
								>{form.quota_key}</option
							>{/if}
						{#each quotas.data ?? [] as qp (qp.id)}<option value={qp.key}
								>{qp.name} ({qp.key})</option
							>{/each}
					</select>
				</Field>
				<fieldset class="sm:col-span-2">
					<legend class="label">Roles</legend>
					{#if !roles.data}
						<Skeleton lines={2} />
					{:else}
						<div class="grid gap-2 sm:grid-cols-2">
							{#each roles.data as r (r.id)}
								{@const grantable = canEditRoles && canGrant(r.rank, maxRank)}
								<label
									class="flex items-start gap-2 rounded-md border border-border p-2.5 text-sm {grantable
										? 'hover:bg-panel-2'
										: 'opacity-70'}"
								>
									<input
										type="checkbox"
										class="mt-0.5"
										value={r.key}
										bind:group={form.roles}
										disabled={!grantable}
									/>
									<span>
										<span class="font-medium">{r.name}</span>
										<span class="text-xs text-fg-muted">rank {r.rank}</span>
										{#if r.requires_mfa}<Badge tone="warning">MFA</Badge>{/if}
									</span>
								</label>
							{/each}
						</div>
						<p class="mt-1 text-xs text-fg-muted">
							{#if isSelf}You cannot change your own roles.{:else if !canEditRoles}You cannot change
								this user’s roles.{:else}Roles at or above your own rank are locked.{/if}
						</p>
					{/if}
					{#if errors.roles}<p class="mt-1 text-xs text-danger" role="alert">{errors.roles}</p>{/if}
				</fieldset>
				{#if canEdit}
					<div class="flex justify-end gap-2 sm:col-span-2">
						<Button
							disabled={!dirty || saving}
							onclick={() => {
								loadedFor = '';
								errors = {};
							}}>Discard changes</Button
						>
						<Button type="submit" variant="primary" loading={saving} disabled={!dirty}
							>Save changes</Button
						>
					</div>
				{/if}
			</form>
		</Card>

		<div class="space-y-6">
			<Card title="Account">
				<dl class="space-y-2 text-sm">
					<div class="flex justify-between gap-2">
						<dt class="text-fg-muted">Last login</dt>
						<dd title={dateTime(u.last_login_at)}>
							{u.last_login_at ? relative(u.last_login_at) : 'Never'}
						</dd>
					</div>
					<div class="flex justify-between gap-2">
						<dt class="text-fg-muted">Created</dt>
						<dd>{dateTime(u.created_at)}</dd>
					</div>
					{#if u.locked_until && new Date(u.locked_until) > new Date()}
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Locked until</dt>
							<dd class="text-warning">{dateTime(u.locked_until)}</dd>
						</div>
					{/if}
				</dl>
				{#if !isSelf}
					<div class="mt-4 grid gap-2 border-t border-border pt-4">
						{#if auth.can('users.suspend') && canManageTarget}
							{#if u.status === 'active'}
								<Button
									variant="danger"
									class="justify-start"
									loading={busy === 'suspend'}
									onclick={suspend}><UserX class="size-4" /> Suspend user</Button
								>
							{:else}
								<Button class="justify-start" loading={busy === 'activate'} onclick={activate}
									><Play class="size-4" /> Activate user</Button
								>
							{/if}
						{/if}
						{#if auth.can('users.reset_password') && canManageTarget}
							<Button class="justify-start" loading={busy === 'reset'} onclick={resetPassword}
								><KeyRound class="size-4" /> Reset password</Button
							>
							{#if u.mfa_enabled}
								<Button class="justify-start" loading={busy === 'mfa'} onclick={resetMfa}
									><ShieldOff class="size-4" /> Reset MFA</Button
								>
							{/if}
						{/if}
						{#if !canManageTarget}
							<p class="text-xs text-fg-muted">
								Account actions are unavailable for users with an equal or higher role.
							</p>
						{/if}
					</div>
				{:else}
					<p class="mt-4 border-t border-border pt-4 text-xs text-fg-muted">
						Manage your own password and MFA on the <a href="/account" class="link">My Account</a> page.
					</p>
				{/if}
			</Card>

			<Card title="Resource usage">
				{#if usage.error}
					<ErrorState error={usage.error} retry={usage.reload} />
				{:else if !usage.data}
					<Skeleton lines={4} />
				{:else}
					{@const q = usage.data.quota}
					{@const us = usage.data.usage}
					<p class="mb-3 text-xs text-fg-muted">Quota package: {q.name}</p>
					<div class="space-y-3">
						<Meter
							label="Workspaces"
							value={us.workspaces}
							max={q.max_workspaces}
							display="{us.workspaces} / {q.max_workspaces}"
						/>
						<Meter
							label="Running CPU"
							value={us.running_cpu_millis}
							max={q.cpu_millis}
							display="{cores(us.running_cpu_millis)} / {cores(q.cpu_millis)}"
						/>
						<Meter
							label="Running memory"
							value={us.running_memory_bytes}
							max={q.memory_bytes}
							display="{bytes(us.running_memory_bytes)} / {bytes(q.memory_bytes)}"
						/>
						<Meter
							label="Storage allocated"
							value={us.allocated_storage_bytes}
							max={q.storage_bytes}
							display="{bytes(us.allocated_storage_bytes)} / {bytes(q.storage_bytes)}"
						/>
						<Meter
							label="Databases"
							value={us.databases}
							max={q.max_databases}
							display="{us.databases} / {q.max_databases}"
						/>
					</div>
					<p class="mt-3 text-xs text-fg-muted">
						{us.containers} containers · {us.terminal_sessions} terminal sessions · {us.active_gpu_allocations}
						GPU allocations · {bytes(us.used_storage_bytes)} storage used
					</p>
				{/if}
			</Card>
		</div>
	</div>

	{#if auth.can('audit.view')}
		<Card title="Recent activity" class="mt-6" padded={false}>
			{#snippet actions()}<a href="/admin/audit?actor_id={u.id}" class="link text-sm"
					>Open in audit log</a
				>{/snippet}
			{#if audit.error}
				<ErrorState error={audit.error} retry={audit.reload} />
			{:else if !audit.data}
				<div class="p-5"><Skeleton lines={4} /></div>
			{:else if audit.data.data.length === 0}
				<EmptyState title="No activity recorded" description="Actions by this user appear here." />
			{:else}
				<ul class="divide-y divide-border">
					{#each audit.data.data as a (a.id)}
						<li class="flex items-center justify-between gap-3 px-5 py-2.5 text-sm">
							<span class="min-w-0 truncate">
								<span class="font-mono text-xs">{a.action}</span>
								<span class="text-fg-muted"
									>· {a.resource_type}{#if a.resource_id}
										<span class="font-mono text-xs">{a.resource_id.slice(0, 8)}</span>{/if}</span
								>
							</span>
							<span class="flex shrink-0 items-center gap-2 text-xs text-fg-muted">
								{#if a.outcome !== 'success'}<Badge
										tone={a.outcome === 'denied' ? 'warning' : 'danger'}>{a.outcome}</Badge
									>{/if}
								<span title={dateTime(a.occurred_at)}>{relative(a.occurred_at)}</span>
							</span>
						</li>
					{/each}
				</ul>
			{/if}
		</Card>
	{/if}
{/if}

<Modal bind:open={resetOpen} title="Password reset link" size="md" onclose={closeReset}>
	{#if resetResult}
		<p class="mb-4 text-sm text-fg-muted">
			Send this single-use link to the user. It expires in {Math.round(
				resetResult.expires_in_seconds / 60
			)} minutes. All of the user’s sessions were signed out.
		</p>
		{#if resetResult.emailed}
			<div class="mb-4">
				<Alert tone="success" title="Emailed"
					>The link was also sent to the user’s email address.</Alert
				>
			</div>
		{:else}
			<div class="mb-4">
				<Alert tone="info" title="Not emailed"
					>Email delivery is not configured or failed. Deliver the link yourself.</Alert
				>
			</div>
		{/if}
		<SecretOnce label="Reset link" value={resetResult.reset_link} />
	{/if}
	{#snippet footer()}<Button variant="primary" onclick={closeReset}>Done</Button>{/snippet}
</Modal>
