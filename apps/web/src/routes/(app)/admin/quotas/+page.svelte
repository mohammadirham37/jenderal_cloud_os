<script lang="ts">
	import { Gauge, Pencil, Plus, Trash2 } from '@lucide/svelte';
	import {
		Alert,
		Badge,
		Button,
		Card,
		DataTable,
		EmptyState,
		Field,
		Modal,
		PageHeader,
		Switch
	} from '#lib/components/ui/index.ts';
	import { api, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Quota } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes, cores, GiB } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import { guarded } from '#lib/components/admin/access.ts';
	import { handleFormError, num } from '#lib/components/admin/forms.ts';

	const perms = ['quotas.view', 'quotas.manage'];
	const allowed = $derived(auth.can(...perms));
	const canManage = $derived(auth.can('quotas.manage'));
	const quotas = resource(guarded(perms, () => api.get<Quota[]>('/quotas')));

	const minutes = (m: number | null) => {
		if (m === null) return 'No limit';
		if (m % 1440 === 0) return `${m / 1440} d`;
		if (m % 60 === 0) return `${m / 60} h`;
		return `${m} min`;
	};

	// Form (cores / GiB in the UI; millis / bytes in the API).
	interface Form {
		key: string;
		name: string;
		description: string;
		cpu: number | string;
		memory: number | string;
		storage: number | string;
		max_workspaces: number | string;
		max_containers: number | string;
		max_databases: number | string;
		max_terminal_sessions: number | string;
		gpu_allowed: boolean;
		idle_timeout_min: number | string;
		max_workspace_duration_min: number | string;
	}
	let open = $state(false);
	let editing = $state<Quota | null>(null);
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let form = $state<Form>(blank());

	function blank(): Form {
		return {
			key: '',
			name: '',
			description: '',
			cpu: 2,
			memory: 4,
			storage: 10,
			max_workspaces: 2,
			max_containers: 4,
			max_databases: 1,
			max_terminal_sessions: 3,
			gpu_allowed: false,
			idle_timeout_min: 120,
			max_workspace_duration_min: 0
		};
	}
	function openCreate() {
		editing = null;
		errors = {};
		form = blank();
		open = true;
	}
	function openEdit(q: Quota) {
		editing = q;
		errors = {};
		form = {
			key: q.key,
			name: q.name,
			description: q.description,
			cpu: q.cpu_millis / 1000,
			memory: +(q.memory_bytes / GiB).toFixed(3),
			storage: +(q.storage_bytes / GiB).toFixed(3),
			max_workspaces: q.max_workspaces,
			max_containers: q.max_containers,
			max_databases: q.max_databases,
			max_terminal_sessions: q.max_terminal_sessions,
			gpu_allowed: q.gpu_allowed,
			idle_timeout_min: q.idle_timeout_min ?? 0,
			max_workspace_duration_min: q.max_workspace_duration_min ?? 0
		};
		open = true;
	}

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		errors = {};
		const local: Record<string, string> = {};
		const cpu = num(form.cpu);
		const mem = num(form.memory);
		const sto = num(form.storage);
		if (cpu === null || cpu <= 0) local.cpu_millis = 'Enter the number of CPU cores.';
		if (mem === null || mem <= 0) local.memory_bytes = 'Enter the memory in GiB.';
		if (sto === null || sto <= 0) local.storage_bytes = 'Enter the storage in GiB.';
		const ints: Record<string, number> = {};
		for (const k of [
			'max_workspaces',
			'max_containers',
			'max_databases',
			'max_terminal_sessions',
			'idle_timeout_min',
			'max_workspace_duration_min'
		] as const) {
			const v = num(form[k]);
			if (v === null || !Number.isInteger(v) || v < 0)
				local[k] = 'Enter a whole number (0 or more).';
			else ints[k] = v;
		}
		if (Object.keys(local).length) {
			errors = local;
			return;
		}
		const body = {
			name: form.name.trim(),
			description: form.description.trim(),
			cpu_millis: Math.round((cpu as number) * 1000),
			memory_bytes: Math.round((mem as number) * GiB),
			storage_bytes: Math.round((sto as number) * GiB),
			gpu_allowed: form.gpu_allowed,
			...ints
		};
		saving = true;
		try {
			if (editing) {
				await api.patch<Quota>(`/quotas/${editing.id}`, body);
				toast.success('Quota package updated', body.name);
			} else {
				await api.post<Quota>('/quotas', { ...body, key: form.key.trim() });
				toast.success('Quota package created', body.name);
			}
			open = false;
			quotas.reload();
		} catch (err) {
			errors = handleFormError(err, 'Could not save the quota package');
		} finally {
			saving = false;
		}
	}

	async function remove(q: Quota) {
		const inUse = q.user_count + q.project_count;
		const ok = await confirmDialog.confirm({
			title: `Delete quota package “${q.name}”?`,
			message: inUse
				? `It is assigned to ${q.user_count} user(s) and ${q.project_count} project(s). Reassign them first; the server will refuse otherwise.`
				: 'The package will be removed permanently. The default package cannot be deleted.',
			danger: true,
			confirmLabel: 'Delete package'
		});
		if (!ok) return;
		try {
			await api.del(`/quotas/${q.id}`);
			toast.success('Quota package deleted', q.name);
			quotas.reload();
		} catch (err) {
			toast.error('Could not delete the quota package', errorMessage(err));
		}
	}

	const columns = $derived([
		{ key: 'name', label: 'Package' },
		{ key: 'cpu', label: 'CPU' },
		{ key: 'ram', label: 'RAM' },
		{ key: 'storage', label: 'Storage' },
		{ key: 'limits', label: 'Workspaces / containers / DBs / terminals' },
		{ key: 'gpu', label: 'GPU' },
		{ key: 'idle', label: 'Idle timeout' },
		{ key: 'duration', label: 'Max duration' },
		{ key: 'usage', label: 'Users / projects' },
		...(canManage ? [{ key: 'actions', label: 'Actions', class: 'text-right' }] : [])
	]);
</script>

<PageHeader
	title="Quotas"
	description="Resource packages assigned to users and projects. Limits apply to running workloads and allocations."
>
	{#snippet actions()}
		{#if allowed && canManage}
			<Button variant="primary" onclick={openCreate}><Plus class="size-4" /> New package</Button>
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else}
	<div class="mb-6">
		<Alert tone="info" title="Tune packages to your server">
			The default packages are examples. Adjust CPU, memory and storage to the real capacity of the
			host (see Host &amp; Agent) and to how many users work at the same time. CPU may be
			overcommitted (Platform Settings → scheduler); memory and storage should not be.
		</Alert>
	</div>
	<Card padded={false}>
		<DataTable
			{columns}
			rows={quotas.data ?? []}
			loading={quotas.loading}
			error={quotas.error}
			retry={quotas.reload}
		>
			{#snippet empty()}
				<EmptyState
					icon={Gauge}
					title="No quota packages"
					description="Create a package to assign resources to users."
				>
					{#snippet action()}{#if canManage}<Button variant="primary" onclick={openCreate}
								><Plus class="size-4" /> New package</Button
							>{/if}{/snippet}
				</EmptyState>
			{/snippet}
			{#snippet row(q)}
				<tr>
					<td class="px-4 py-3">
						<p class="font-medium">{q.name}</p>
						<p class="font-mono text-xs text-fg-muted">{q.key}</p>
						{#if q.description}<p class="max-w-xs text-xs text-fg-muted">{q.description}</p>{/if}
					</td>
					<td class="px-4 py-3 whitespace-nowrap tabular-nums">{cores(q.cpu_millis)}</td>
					<td class="px-4 py-3 whitespace-nowrap tabular-nums">{bytes(q.memory_bytes)}</td>
					<td class="px-4 py-3 whitespace-nowrap tabular-nums">{bytes(q.storage_bytes)}</td>
					<td class="px-4 py-3 tabular-nums"
						>{q.max_workspaces} / {q.max_containers} / {q.max_databases} / {q.max_terminal_sessions}</td
					>
					<td class="px-4 py-3"
						>{#if q.gpu_allowed}<Badge tone="success">Allowed</Badge>{:else}<Badge>No</Badge
							>{/if}</td
					>
					<td class="px-4 py-3 whitespace-nowrap">{minutes(q.idle_timeout_min)}</td>
					<td class="px-4 py-3 whitespace-nowrap">{minutes(q.max_workspace_duration_min)}</td>
					<td class="px-4 py-3 tabular-nums">{q.user_count} / {q.project_count}</td>
					{#if canManage}
						<td class="px-4 py-3 text-right whitespace-nowrap">
							<Button
								size="sm"
								variant="ghost"
								onclick={() => openEdit(q)}
								aria-label="Edit {q.name}"><Pencil class="size-3.5" /> Edit</Button
							>
							<Button
								size="sm"
								variant="ghost"
								onclick={() => remove(q)}
								aria-label="Delete {q.name}"><Trash2 class="size-3.5 text-danger" /></Button
							>
						</td>
					{/if}
				</tr>
			{/snippet}
		</DataTable>
	</Card>
{/if}

<Modal
	bind:open
	title={editing ? `Edit package: ${editing.name}` : 'New quota package'}
	description={editing
		? 'Changes apply to new allocations; running workloads keep their current limits.'
		: undefined}
	size="lg"
>
	<form id="quota-form" class="grid gap-4 sm:grid-cols-2" onsubmit={submit}>
		{#if !editing}
			<Field
				label="Key"
				id="q-key"
				required
				error={errors.key}
				hint="2-63 lowercase letters, digits, '-' or '_'. Cannot be changed later."
			>
				<input id="q-key" class="input font-mono" bind:value={form.key} required />
			</Field>
		{/if}
		<Field label="Name" id="q-name" required error={errors.name}>
			<input id="q-name" class="input" bind:value={form.name} maxlength="80" required />
		</Field>
		<div class="sm:col-span-2">
			<Field label="Description" id="q-desc" error={errors.description}>
				<input id="q-desc" class="input" bind:value={form.description} />
			</Field>
		</div>
		<Field label="CPU (cores)" id="q-cpu" required error={errors.cpu_millis} hint="0.1 – 256">
			<input
				id="q-cpu"
				type="number"
				class="input"
				min="0.1"
				max="256"
				step="0.1"
				bind:value={form.cpu}
			/>
		</Field>
		<Field label="Memory (GiB)" id="q-mem" required error={errors.memory_bytes} hint="0.125 – 2048">
			<input
				id="q-mem"
				type="number"
				class="input"
				min="0.125"
				step="0.125"
				bind:value={form.memory}
			/>
		</Field>
		<Field
			label="Storage (GiB)"
			id="q-sto"
			required
			error={errors.storage_bytes}
			hint="0.25 – 65536"
		>
			<input
				id="q-sto"
				type="number"
				class="input"
				min="0.25"
				step="0.25"
				bind:value={form.storage}
			/>
		</Field>
		<Field label="Max workspaces" id="q-ws" required error={errors.max_workspaces}>
			<input
				id="q-ws"
				type="number"
				class="input"
				min="0"
				max="1000"
				step="1"
				bind:value={form.max_workspaces}
			/>
		</Field>
		<Field label="Max containers" id="q-ct" required error={errors.max_containers}>
			<input
				id="q-ct"
				type="number"
				class="input"
				min="0"
				max="1000"
				step="1"
				bind:value={form.max_containers}
			/>
		</Field>
		<Field label="Max databases" id="q-db" required error={errors.max_databases}>
			<input
				id="q-db"
				type="number"
				class="input"
				min="0"
				max="1000"
				step="1"
				bind:value={form.max_databases}
			/>
		</Field>
		<Field label="Max terminal sessions" id="q-term" required error={errors.max_terminal_sessions}>
			<input
				id="q-term"
				type="number"
				class="input"
				min="0"
				max="1000"
				step="1"
				bind:value={form.max_terminal_sessions}
			/>
		</Field>
		<Field
			label="Idle timeout (minutes)"
			id="q-idle"
			error={errors.idle_timeout_min}
			hint="0 = never stop idle workspaces"
		>
			<input
				id="q-idle"
				type="number"
				class="input"
				min="0"
				max="525600"
				step="1"
				bind:value={form.idle_timeout_min}
			/>
		</Field>
		<Field
			label="Max workspace run time (minutes)"
			id="q-dur"
			error={errors.max_workspace_duration_min}
			hint="0 = no limit"
		>
			<input
				id="q-dur"
				type="number"
				class="input"
				min="0"
				max="525600"
				step="1"
				bind:value={form.max_workspace_duration_min}
			/>
		</Field>
		<div class="flex items-center gap-3 sm:col-span-2">
			<Switch id="q-gpu" label="GPU allowed" bind:checked={form.gpu_allowed} />
			<label for="q-gpu" class="text-sm">Users with this package may request GPU access</label>
		</div>
	</form>
	{#snippet footer()}
		<Button onclick={() => (open = false)}>Cancel</Button>
		<Button type="submit" form="quota-form" variant="primary" loading={saving}
			>{editing ? 'Save package' : 'Create package'}</Button
		>
	{/snippet}
</Modal>
