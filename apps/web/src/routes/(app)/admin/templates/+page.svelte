<script lang="ts">
	import { LayoutTemplate, Pencil, Plus, Trash2 } from '@lucide/svelte';
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
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Template } from '#lib/api/types.ts';
	import type { ContainerImage } from '#lib/api/types-admin.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes, cores, GiB, titleCase } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import KeyValueEditor from '#lib/components/admin/KeyValueEditor.svelte';
	import { guarded } from '#lib/components/admin/access.ts';
	import { handleFormError, num } from '#lib/components/admin/forms.ts';
	import { toRecord, toRows, type KV } from '#lib/components/admin/kv.ts';

	const allowed = $derived(auth.can('templates.manage'));
	const templates = resource(
		guarded(['templates.manage'], () => api.get<Template[]>('/templates', { all: 'true' }))
	);
	const images = resource(
		guarded(['images.view', 'images.manage'], () => api.get<ContainerImage[]>('/images'))
	);

	interface PortRow {
		port: number | string;
		name: string;
		proxy: boolean;
	}
	interface Form {
		key: string;
		name: string;
		description: string;
		category: string;
		image_id: string;
		runtime: KV[];
		min_cpu: number | string;
		min_mem: number | string;
		rec_cpu: number | string;
		rec_mem: number | string;
		storage: number | string;
		ports: PortRow[];
		gpu_mode: 'none' | 'optional' | 'required';
		env: KV[];
		command: string;
		read_only_rootfs: boolean;
		is_active: boolean;
	}

	let open = $state(false);
	let editing = $state<Template | null>(null);
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let form = $state<Form>(blank());

	function blank(): Form {
		return {
			key: '',
			name: '',
			description: '',
			category: 'general',
			image_id: '',
			runtime: [],
			min_cpu: 0.5,
			min_mem: 0.5,
			rec_cpu: 1,
			rec_mem: 2,
			storage: 5,
			ports: [],
			gpu_mode: 'none',
			env: [],
			command: '',
			read_only_rootfs: false,
			is_active: true
		};
	}
	const round = (v: number) => +v.toFixed(3);
	function openCreate() {
		editing = null;
		errors = {};
		form = blank();
		open = true;
	}
	function openEdit(t: Template) {
		editing = t;
		errors = {};
		form = {
			key: t.key,
			name: t.name,
			description: t.description,
			category: t.category,
			image_id: t.image_id,
			runtime: toRows(t.runtime_versions),
			min_cpu: t.min_cpu_millis / 1000,
			min_mem: round(t.min_memory_bytes / GiB),
			rec_cpu: t.rec_cpu_millis / 1000,
			rec_mem: round(t.rec_memory_bytes / GiB),
			storage: round(t.default_storage_bytes / GiB),
			ports: (t.ports ?? []).map((p) => ({ port: p.port, name: p.name, proxy: !!p.proxy })),
			gpu_mode: t.gpu_mode,
			env: toRows(t.env),
			command: (t.command ?? []).join('\n'),
			read_only_rootfs: t.security?.read_only_rootfs ?? false,
			is_active: t.is_active
		};
		open = true;
	}

	// Allowed images, plus the current image when editing (even if disallowed).
	const imageOptions = $derived(
		(images.data ?? []).filter((i) => i.is_allowed || i.id === editing?.image_id)
	);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		errors = {};
		const local: Record<string, string> = {};
		const v = {
			min_cpu: num(form.min_cpu),
			rec_cpu: num(form.rec_cpu),
			min_mem: num(form.min_mem),
			rec_mem: num(form.rec_mem),
			storage: num(form.storage)
		};
		if (v.min_cpu === null) local.min_cpu_millis = 'Required.';
		if (v.rec_cpu === null) local.rec_cpu_millis = 'Required.';
		if (v.min_mem === null) local.min_memory_bytes = 'Required.';
		if (v.rec_mem === null) local.rec_memory_bytes = 'Required.';
		if (v.storage === null) local.default_storage_bytes = 'Required.';
		if (!form.image_id) local.image_id = 'Select an image.';
		const ports = form.ports
			.filter((p) => String(p.port).trim() !== '')
			.map((p) => ({ port: Number(p.port), name: p.name.trim(), proxy: p.proxy }));
		if (ports.some((p) => !Number.isInteger(p.port) || p.port < 1024 || p.port > 65535))
			local.ports = 'Ports must be whole numbers between 1024 and 65535.';
		if (Object.keys(local).length) {
			errors = local;
			return;
		}
		const body = {
			name: form.name.trim(),
			description: form.description.trim(),
			category: form.category.trim() || 'general',
			image_id: form.image_id,
			runtime_versions: toRecord(form.runtime),
			min_cpu_millis: Math.round((v.min_cpu as number) * 1000),
			rec_cpu_millis: Math.round((v.rec_cpu as number) * 1000),
			min_memory_bytes: Math.round((v.min_mem as number) * GiB),
			rec_memory_bytes: Math.round((v.rec_mem as number) * GiB),
			default_storage_bytes: Math.round((v.storage as number) * GiB),
			ports,
			gpu_mode: form.gpu_mode,
			env: toRecord(form.env),
			command: form.command
				.split('\n')
				.map((s) => s.trim())
				.filter(Boolean),
			read_only_rootfs: form.read_only_rootfs,
			is_active: form.is_active
		};
		saving = true;
		try {
			if (editing) {
				await api.patch<Template>(`/templates/${editing.id}`, body);
				toast.success('Template updated', body.name);
			} else {
				await api.post<Template>('/templates', { ...body, key: form.key.trim() });
				toast.success('Template created', body.name);
			}
			open = false;
			templates.reload();
		} catch (err) {
			errors = handleFormError(err, 'Could not save the template');
		} finally {
			saving = false;
		}
	}

	const columns = [
		{ key: 'name', label: 'Template' },
		{ key: 'image', label: 'Image' },
		{ key: 'resources', label: 'Min / recommended' },
		{ key: 'storage', label: 'Storage' },
		{ key: 'gpu', label: 'GPU' },
		{ key: 'ports', label: 'Ports' },
		{ key: 'status', label: 'Status' },
		{ key: 'actions', label: 'Actions', class: 'text-right' }
	];
</script>

<PageHeader
	title="Template Management"
	description="Workspace templates define the image, default resources, ports and environment. Existing workspaces keep the limits they were created with."
>
	{#snippet actions()}
		{#if allowed}
			<Button variant="primary" onclick={openCreate}><Plus class="size-4" /> New template</Button>
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else}
	<Card padded={false}>
		<DataTable
			{columns}
			rows={templates.data ?? []}
			loading={templates.loading}
			error={templates.error}
			retry={templates.reload}
		>
			{#snippet empty()}
				<EmptyState
					icon={LayoutTemplate}
					title="No templates yet"
					description="Add an image to the allowlist first, then create a template that uses it."
				>
					{#snippet action()}<Button variant="primary" onclick={openCreate}
							><Plus class="size-4" /> New template</Button
						>{/snippet}
				</EmptyState>
			{/snippet}
			{#snippet row(t)}
				<tr class={t.is_active ? '' : 'opacity-70'}>
					<td class="px-4 py-3">
						<p class="font-medium">{t.name}</p>
						<p class="text-xs text-fg-muted">
							<span class="font-mono">{t.key}</span> · {titleCase(t.category)}
						</p>
					</td>
					<td class="px-4 py-3">
						<p class="max-w-[16rem] truncate font-mono text-xs" title={t.image_reference}>
							{t.image_reference}
						</p>
						<div class="mt-1 flex flex-wrap gap-1">
							{#if !t.image_allowed}<Badge tone="danger">Not allowed</Badge>{/if}
							{#if !t.image_present}<Badge tone="warning">Not pulled</Badge>{/if}
						</div>
					</td>
					<td class="px-4 py-3 text-xs whitespace-nowrap">
						{cores(t.min_cpu_millis)} · {bytes(t.min_memory_bytes)}<br />
						<span class="text-fg-muted"
							>{cores(t.rec_cpu_millis)} · {bytes(t.rec_memory_bytes)}</span
						>
					</td>
					<td class="px-4 py-3 whitespace-nowrap">{bytes(t.default_storage_bytes)}</td>
					<td class="px-4 py-3">{t.gpu_mode === 'none' ? '—' : titleCase(t.gpu_mode)}</td>
					<td class="px-4 py-3 font-mono text-xs"
						>{(t.ports ?? []).map((p) => p.port).join(', ') || '—'}</td
					>
					<td class="px-4 py-3"
						>{#if t.is_active}<Badge tone="success" dot>Active</Badge>{:else}<Badge dot
								>Inactive</Badge
							>{/if}</td
					>
					<td class="px-4 py-3 text-right">
						<Button size="sm" variant="ghost" onclick={() => openEdit(t)} aria-label="Edit {t.name}"
							><Pencil class="size-3.5" /> Edit</Button
						>
					</td>
				</tr>
			{/snippet}
		</DataTable>
	</Card>
{/if}

<Modal
	bind:open
	title={editing ? `Edit template: ${editing.name}` : 'New template'}
	description={editing
		? 'Changes apply to workspaces created from now on.'
		: 'Only allow-listed images can be used.'}
	size="xl"
>
	<form id="tpl-form" class="space-y-6" onsubmit={submit}>
		<section class="grid gap-4 sm:grid-cols-2">
			{#if !editing}
				<Field
					label="Key"
					id="t-key"
					required
					error={errors.key}
					hint="2-63 lowercase letters, digits or '-'. Cannot be changed later."
				>
					<input id="t-key" class="input font-mono" bind:value={form.key} required />
				</Field>
			{/if}
			<Field label="Name" id="t-name" required error={errors.name}>
				<input id="t-name" class="input" bind:value={form.name} maxlength="80" required />
			</Field>
			<Field
				label="Category"
				id="t-cat"
				error={errors.category}
				hint="e.g. general, data-science, web"
			>
				<input id="t-cat" class="input" bind:value={form.category} />
			</Field>
			<Field label="Image" id="t-image" required error={errors.image_id}>
				<select id="t-image" class="input" bind:value={form.image_id} required>
					<option value="" disabled>Select an allowed image…</option>
					{#each imageOptions as i (i.id)}
						<option value={i.id}
							>{i.name} — {i.reference}{i.is_allowed ? '' : ' (not allowed)'}{i.pulled_at
								? ''
								: ' (not pulled)'}</option
						>
					{/each}
				</select>
			</Field>
			<div class="sm:col-span-2">
				<Field label="Description" id="t-desc" error={errors.description}>
					<textarea id="t-desc" class="input" rows="2" bind:value={form.description}></textarea>
				</Field>
			</div>
			{#if images.error}
				<div class="sm:col-span-2">
					<Alert tone="warning" title="Images could not be loaded"
						>The image list requires the images.view permission.</Alert
					>
				</div>
			{/if}
		</section>

		<section>
			<h3 class="mb-3 text-sm font-semibold">Resources</h3>
			<div class="grid gap-4 sm:grid-cols-3">
				<Field label="Min CPU (cores)" id="t-mincpu" required error={errors.min_cpu_millis}>
					<input
						id="t-mincpu"
						type="number"
						class="input"
						min="0.1"
						max="64"
						step="0.1"
						bind:value={form.min_cpu}
					/>
				</Field>
				<Field label="Recommended CPU (cores)" id="t-reccpu" required error={errors.rec_cpu_millis}>
					<input
						id="t-reccpu"
						type="number"
						class="input"
						min="0.1"
						max="64"
						step="0.1"
						bind:value={form.rec_cpu}
					/>
				</Field>
				<Field
					label="Default storage (GiB)"
					id="t-sto"
					required
					error={errors.default_storage_bytes}
					hint="At least 0.25 GiB"
				>
					<input
						id="t-sto"
						type="number"
						class="input"
						min="0.25"
						step="0.25"
						bind:value={form.storage}
					/>
				</Field>
				<Field
					label="Min memory (GiB)"
					id="t-minmem"
					required
					error={errors.min_memory_bytes}
					hint="At least 0.0625 GiB (64 MiB)"
				>
					<input
						id="t-minmem"
						type="number"
						class="input"
						min="0.0625"
						step="0.0625"
						bind:value={form.min_mem}
					/>
				</Field>
				<Field
					label="Recommended memory (GiB)"
					id="t-recmem"
					required
					error={errors.rec_memory_bytes}
				>
					<input
						id="t-recmem"
						type="number"
						class="input"
						min="0.0625"
						step="0.0625"
						bind:value={form.rec_mem}
					/>
				</Field>
				<Field label="GPU mode" id="t-gpu" error={errors.gpu_mode}>
					<select id="t-gpu" class="input" bind:value={form.gpu_mode}>
						<option value="none">None</option>
						<option value="optional">Optional</option>
						<option value="required">Required</option>
					</select>
				</Field>
			</div>
		</section>

		<section>
			<h3 class="mb-1 text-sm font-semibold">Ports</h3>
			<p class="mb-3 text-xs text-fg-muted">
				Container ports (1024-65535). Proxied ports are reachable through the workspace app proxy.
			</p>
			{#if errors.ports}<p class="mb-2 text-xs text-danger" role="alert">{errors.ports}</p>{/if}
			<div class="space-y-2">
				{#each form.ports as p, i (i)}
					<div class="flex flex-wrap items-center gap-2">
						<input
							type="number"
							class="input w-28"
							min="1024"
							max="65535"
							placeholder="8080"
							aria-label="Port {i + 1} number"
							bind:value={p.port}
						/>
						<input
							class="input w-48"
							placeholder="Name (e.g. web)"
							maxlength="40"
							aria-label="Port {i + 1} name"
							bind:value={p.name}
						/>
						<label class="flex items-center gap-2 text-sm">
							<input type="checkbox" bind:checked={p.proxy} /> Proxy
						</label>
						<Button
							size="sm"
							variant="ghost"
							aria-label="Remove port {i + 1}"
							onclick={() => (form.ports = form.ports.filter((_, j) => j !== i))}
							><Trash2 class="size-4" /></Button
						>
					</div>
				{/each}
				<Button
					size="sm"
					disabled={form.ports.length >= 16}
					onclick={() => (form.ports = [...form.ports, { port: '', name: '', proxy: true }])}
					><Plus class="size-3.5" /> Add port</Button
				>
			</div>
		</section>

		<section class="grid gap-6 md:grid-cols-2">
			<div>
				<h3 class="mb-1 text-sm font-semibold">Runtime versions</h3>
				<p class="mb-3 text-xs text-fg-muted">Shown to users, e.g. python → 3.12.</p>
				{#if errors.runtime_versions}<p class="mb-2 text-xs text-danger" role="alert">
						{errors.runtime_versions}
					</p>{/if}
				<KeyValueEditor
					id="t-runtime"
					bind:rows={form.runtime}
					keyPlaceholder="python"
					valuePlaceholder="3.12"
					addLabel="Add runtime"
				/>
			</div>
			<div>
				<h3 class="mb-1 text-sm font-semibold">Environment variables</h3>
				<p class="mb-3 text-xs text-fg-muted">
					Names in UPPER_CASE. Do not store secrets here; they are visible to administrators.
				</p>
				{#if errors.env}<p class="mb-2 text-xs text-danger" role="alert">{errors.env}</p>{/if}
				<KeyValueEditor id="t-env" bind:rows={form.env} addLabel="Add variable" />
			</div>
		</section>

		<section class="grid gap-4 sm:grid-cols-2">
			<Field
				label="Command"
				id="t-cmd"
				error={errors.command}
				hint="One argument per line. Leave empty to use the image default."
			>
				<textarea id="t-cmd" class="input font-mono" rows="4" bind:value={form.command}></textarea>
			</Field>
			<div class="space-y-4">
				<div class="flex items-center gap-3">
					<Switch
						id="t-ro"
						label="Read-only root filesystem"
						bind:checked={form.read_only_rootfs}
					/>
					<label for="t-ro" class="text-sm">Read-only root filesystem</label>
				</div>
				<div class="flex items-center gap-3">
					<Switch id="t-active" label="Active" bind:checked={form.is_active} />
					<label for="t-active" class="text-sm">Active (visible to users)</label>
				</div>
			</div>
		</section>
	</form>
	{#snippet footer()}
		<Button onclick={() => (open = false)}>Cancel</Button>
		<Button type="submit" form="tpl-form" variant="primary" loading={saving}
			>{editing ? 'Save template' : 'Create template'}</Button
		>
	{/snippet}
</Modal>
