<script lang="ts">
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import {
		Alert,
		Badge,
		Button,
		Card,
		ErrorState,
		Field,
		Meter,
		PageHeader,
		Skeleton,
		Tabs
	} from '#lib/components/ui/index.ts';
	import SecurityPanel from '#lib/components/account/SecurityPanel.svelte';
	import SessionsPanel from '#lib/components/account/SessionsPanel.svelte';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Quota, Usage } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes, cores, dateTime, titleCase } from '#lib/format.ts';

	const tabs = [
		{ id: 'profile', label: 'Profile' },
		{ id: 'security', label: 'Security' },
		{ id: 'sessions', label: 'Sessions' },
		{ id: 'usage', label: 'Usage & quota' }
	];
	// Follows ?tab= in the URL; overwritten locally when a tab is selected.
	let active = $derived.by(() => {
		const t = page.url.searchParams.get('tab');
		return t && tabs.some((x) => x.id === t) ? t : 'profile';
	});
	function select(id: string) {
		active = id;
		goto(`?tab=${id}`, { replace: true, reset: false });
	}

	const me = $derived(auth.me);

	// ---- Profile
	let fullName = $state(auth.me?.full_name ?? '');
	let saving = $state(false);
	let nameError = $state('');
	const dirty = $derived(!!me && fullName.trim() !== me.full_name && fullName.trim() !== '');

	async function saveProfile(e: SubmitEvent) {
		e.preventDefault();
		if (!me) return;
		saving = true;
		nameError = '';
		try {
			await api.patch(`/users/${me.id}`, { full_name: fullName.trim() });
			await auth.load();
			fullName = auth.me?.full_name ?? fullName;
			toast.success('Profile updated');
		} catch (err) {
			nameError =
				err instanceof ApiError && err.details.full_name
					? err.details.full_name
					: errorMessage(err);
		} finally {
			saving = false;
		}
	}

	// ---- Usage
	const usage = resource(() => api.get<{ quota: Quota; usage: Usage }>('/me/usage'));

	function minutes(m: number | null): string {
		if (m === null) return 'No limit';
		if (m % 60 === 0) return `${m / 60} h`;
		return `${m} min`;
	}
</script>

<PageHeader
	title="My Account"
	description="Your profile, sign-in security, sessions and resource quota."
/>

<Tabs {tabs} bind:active onchange={select} />

<div class="mt-6" role="tabpanel" aria-labelledby="tab-{active}">
	{#if active === 'profile'}
		{#if me}
			<div class="grid gap-6 lg:grid-cols-2">
				<Card title="Profile">
					<form class="space-y-4" onsubmit={saveProfile}>
						<Field label="Username" id="acc-username" hint="Usernames cannot be changed.">
							<input id="acc-username" class="input font-mono" value={me.username} readonly />
						</Field>
						<Field
							label="Email"
							id="acc-email"
							hint="Contact an administrator to change your email address."
						>
							<input id="acc-email" class="input" value={me.email} readonly />
						</Field>
						<Field label="Full name" id="acc-fullname" error={nameError || undefined} required>
							<input
								id="acc-fullname"
								class="input"
								bind:value={fullName}
								maxlength="200"
								autocomplete="name"
								required
							/>
						</Field>
						<div class="flex justify-end">
							<Button type="submit" variant="primary" loading={saving} disabled={!dirty}
								>Save changes</Button
							>
						</div>
					</form>
				</Card>
				<Card title="Access">
					<dl class="space-y-3 text-sm">
						<div>
							<dt class="text-fg-muted">Roles</dt>
							<dd class="mt-1 flex flex-wrap gap-1.5">
								{#each me.roles as r (r)}<Badge tone="accent">{titleCase(r)}</Badge>{:else}—{/each}
							</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Permissions</dt>
							<dd>{me.permissions.length}</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Two-factor authentication</dt>
							<dd>{me.mfa_enrolled ? 'Enabled' : 'Not enabled'}</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Session expires</dt>
							<dd>{dateTime(me.session_expires_at)}</dd>
						</div>
					</dl>
					<details class="mt-4 text-sm">
						<summary class="cursor-pointer text-fg-muted">Show permissions</summary>
						<ul class="mt-2 grid gap-1 font-mono text-xs text-fg-muted sm:grid-cols-2">
							{#each [...me.permissions].sort() as p (p)}<li>{p}</li>{/each}
						</ul>
					</details>
				</Card>
			</div>
		{:else}
			<Card><Skeleton lines={4} /></Card>
		{/if}
	{:else if active === 'security'}
		<SecurityPanel />
	{:else if active === 'sessions'}
		<SessionsPanel />
	{:else if active === 'usage'}
		{#if usage.error}
			<Card><ErrorState error={usage.error} retry={usage.reload} /></Card>
		{:else if !usage.data}
			<Card><Skeleton lines={6} /></Card>
		{:else}
			{@const q = usage.data.quota}
			{@const u = usage.data.usage}
			<div class="grid gap-6 lg:grid-cols-3">
				<Card title="Resource usage" description="Quota package: {q.name}" class="lg:col-span-2">
					<div class="grid gap-4 sm:grid-cols-2">
						<Meter
							label="Workspaces"
							value={u.workspaces}
							max={q.max_workspaces}
							display="{u.workspaces} / {q.max_workspaces}"
						/>
						<Meter
							label="Containers"
							value={u.containers}
							max={q.max_containers}
							display="{u.containers} / {q.max_containers}"
						/>
						<Meter
							label="CPU (running)"
							value={u.running_cpu_millis}
							max={q.cpu_millis}
							display="{cores(u.running_cpu_millis)} / {cores(q.cpu_millis)}"
						/>
						<Meter
							label="Memory (running)"
							value={u.running_memory_bytes}
							max={q.memory_bytes}
							display="{bytes(u.running_memory_bytes)} / {bytes(q.memory_bytes)}"
						/>
						<Meter
							label="Storage allocated"
							value={u.allocated_storage_bytes}
							max={q.storage_bytes}
							display="{bytes(u.allocated_storage_bytes)} / {bytes(q.storage_bytes)}"
						/>
						<Meter
							label="Databases"
							value={u.databases}
							max={q.max_databases}
							display="{u.databases} / {q.max_databases}"
						/>
						<Meter
							label="Terminal sessions"
							value={u.terminal_sessions}
							max={q.max_terminal_sessions}
							display="{u.terminal_sessions} / {q.max_terminal_sessions}"
						/>
					</div>
					<p class="mt-4 text-xs text-fg-muted">
						Storage actually used on disk: {bytes(u.used_storage_bytes)}. CPU and memory count only
						running workspaces.
					</p>
				</Card>
				<Card title={q.name}>
					{#if q.description}<p class="mb-4 text-sm text-fg-muted">{q.description}</p>{/if}
					<dl class="space-y-2 text-sm">
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">GPU access</dt>
							<dd>{q.gpu_allowed ? 'Allowed' : 'Not included'}</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Active GPU allocations</dt>
							<dd class="tabular-nums">{u.active_gpu_allocations}</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Max workspace runtime</dt>
							<dd>{minutes(q.max_workspace_duration_min)}</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Idle timeout</dt>
							<dd>{minutes(q.idle_timeout_min)}</dd>
						</div>
					</dl>
					{#if !q.gpu_allowed && auth.can('gpu.request')}
						<div class="mt-4">
							<Alert tone="info">Ask an administrator for a package that includes GPU access.</Alert
							>
						</div>
					{/if}
				</Card>
			</div>
		{/if}
	{/if}
</div>
