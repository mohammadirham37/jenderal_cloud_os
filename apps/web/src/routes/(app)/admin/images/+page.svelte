<script lang="ts">
	import { CloudDownload, HardDrive, Pencil, Plus } from '@lucide/svelte';
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
	import type { ContainerImage } from '#lib/api/types-admin.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes, dateTime, relative } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import { guarded } from '#lib/components/admin/access.ts';
	import { handleFormError } from '#lib/components/admin/forms.ts';

	const perms = ['images.view', 'images.manage'];
	const allowed = $derived(auth.can(...perms));
	const canManage = $derived(auth.can('images.manage'));
	const images = resource(guarded(perms, () => api.get<ContainerImage[]>('/images')));
	const isLocal = (ref: string) => ref.startsWith('jenderal/');

	function replace(img: ContainerImage) {
		if (images.data) images.set(images.data.map((i) => (i.id === img.id ? img : i)));
	}

	// Allow toggle
	let toggling = $state<string | null>(null);
	async function setAllowed(img: ContainerImage, value: boolean) {
		if (!value && img.template_count > 0) {
			const ok = await confirmDialog.confirm({
				title: `Disallow ${img.name}?`,
				message: `${img.template_count} template(s) use this image. They will be hidden from users and new workspaces cannot be created from them. Existing workspaces are not affected.`,
				danger: true,
				confirmLabel: 'Disallow image'
			});
			if (!ok) {
				replace({ ...img });
				return;
			}
		}
		toggling = img.id;
		try {
			replace(await api.patch<ContainerImage>(`/images/${img.id}`, { is_allowed: value }));
			toast.success(value ? 'Image allowed' : 'Image disallowed', img.reference);
		} catch (err) {
			replace({ ...img });
			toast.error('Could not update the image', errorMessage(err));
		} finally {
			toggling = null;
		}
	}

	// Pull / check
	let pulling = $state<Record<string, boolean>>({});
	async function pull(img: ContainerImage) {
		pulling[img.id] = true;
		toast.info(
			'Checking image',
			isLocal(img.reference)
				? `${img.reference} is a locally built image; checking that it exists on the host.`
				: `Pulling ${img.reference} if missing. This can take several minutes.`
		);
		try {
			const updated = await api.post<ContainerImage>(`/images/${img.id}/pull`);
			replace(updated);
			toast.success(
				'Image is available on the host',
				`${img.reference} · ${bytes(updated.size_bytes)}`
			);
		} catch (err) {
			toast.error(
				'Image is not available',
				errorMessage(err) +
					(isLocal(img.reference)
						? ' Build it on the host with installer/build-workspace-images.sh.'
						: '')
			);
		} finally {
			pulling[img.id] = false;
		}
	}

	// Add / edit
	let open = $state(false);
	let editing = $state<ContainerImage | null>(null);
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let form = $state({
		name: '',
		reference: '',
		description: '',
		gpu_capable: false,
		is_allowed: true
	});

	function openCreate() {
		editing = null;
		errors = {};
		form = { name: '', reference: '', description: '', gpu_capable: false, is_allowed: true };
		open = true;
	}
	function openEdit(img: ContainerImage) {
		editing = img;
		errors = {};
		form = {
			name: img.name,
			reference: img.reference,
			description: img.description,
			gpu_capable: img.gpu_capable,
			is_allowed: img.is_allowed
		};
		open = true;
	}
	async function submit(e: SubmitEvent) {
		e.preventDefault();
		errors = {};
		saving = true;
		const body = {
			name: form.name.trim(),
			description: form.description.trim(),
			gpu_capable: form.gpu_capable,
			is_allowed: form.is_allowed
		};
		try {
			if (editing) {
				replace(await api.patch<ContainerImage>(`/images/${editing.id}`, body));
				toast.success('Image updated', body.name);
			} else {
				await api.post<ContainerImage>('/images', { ...body, reference: form.reference.trim() });
				toast.success('Image added', form.reference.trim());
				images.reload();
			}
			open = false;
		} catch (err) {
			errors = handleFormError(err, 'Could not save the image');
		} finally {
			saving = false;
		}
	}

	const columns = $derived([
		{ key: 'name', label: 'Image' },
		{ key: 'reference', label: 'Reference' },
		{ key: 'allowed', label: 'Allowed' },
		{ key: 'pulled', label: 'On host' },
		{ key: 'size', label: 'Size' },
		{ key: 'templates', label: 'Templates' },
		...(canManage ? [{ key: 'actions', label: 'Actions', class: 'text-right' }] : [])
	]);
</script>

<PageHeader
	title="Image Allowlist"
	description="Only allow-listed container images can be used by workspace templates."
>
	{#snippet actions()}
		{#if allowed && canManage}
			<Button variant="primary" onclick={openCreate}><Plus class="size-4" /> Add image</Button>
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else}
	<div class="mb-6">
		<Alert tone="info" title="Local and registry images">
			Images named <code class="font-mono">jenderal/*</code> are built on the server with
			<code class="font-mono">installer/build-workspace-images.sh</code> and cannot be pulled from a registry;
			“Check / pull” only verifies that they exist on the host. Other images are pulled from their registry,
			which can take several minutes.
		</Alert>
	</div>
	<Card padded={false}>
		<DataTable
			{columns}
			rows={images.data ?? []}
			loading={images.loading}
			error={images.error}
			retry={images.reload}
		>
			{#snippet empty()}
				<EmptyState
					icon={HardDrive}
					title="No images on the allowlist"
					description="Add the container images that templates may use."
				>
					{#snippet action()}{#if canManage}<Button variant="primary" onclick={openCreate}
								><Plus class="size-4" /> Add image</Button
							>{/if}{/snippet}
				</EmptyState>
			{/snippet}
			{#snippet row(img)}
				<tr>
					<td class="px-4 py-3">
						<p class="font-medium">
							{img.name}
							{#if img.gpu_capable}<Badge tone="info">GPU</Badge>{/if}
							{#if isLocal(img.reference)}<Badge>Local build</Badge>{/if}
						</p>
						{#if img.description}<p class="max-w-xs text-xs text-fg-muted">
								{img.description}
							</p>{/if}
					</td>
					<td class="px-4 py-3 font-mono text-xs break-all">{img.reference}</td>
					<td class="px-4 py-3">
						{#if canManage}
							{#key img}
								<Switch
									id="allow-{img.id}"
									label="Allow {img.name}"
									checked={img.is_allowed}
									disabled={toggling === img.id}
									onchange={(v) => setAllowed(img, v)}
								/>
							{/key}
						{:else if img.is_allowed}<Badge tone="success">Allowed</Badge>{:else}<Badge
								>Blocked</Badge
							>{/if}
					</td>
					<td class="px-4 py-3 text-xs whitespace-nowrap">
						{#if img.pulled_at}<span title={dateTime(img.pulled_at)}
								>checked {relative(img.pulled_at)}</span
							>{:else}<Badge tone="warning">Not checked</Badge>{/if}
					</td>
					<td class="px-4 py-3 whitespace-nowrap tabular-nums">{bytes(img.size_bytes)}</td>
					<td class="px-4 py-3 tabular-nums">{img.template_count}</td>
					{#if canManage}
						<td class="px-4 py-3 text-right whitespace-nowrap">
							<Button
								size="sm"
								loading={pulling[img.id]}
								onclick={() => pull(img)}
								title={isLocal(img.reference)
									? 'Verify the local image exists'
									: 'Pull from the registry if missing'}
								>{#if !pulling[img.id]}<CloudDownload class="size-3.5" />{/if}
								{pulling[img.id] ? 'Working…' : 'Check / pull'}</Button
							>
							<Button
								size="sm"
								variant="ghost"
								onclick={() => openEdit(img)}
								aria-label="Edit {img.name}"><Pencil class="size-3.5" /></Button
							>
						</td>
					{/if}
				</tr>
			{/snippet}
		</DataTable>
	</Card>
{/if}

<Modal bind:open title={editing ? `Edit image: ${editing.name}` : 'Add image'} size="md">
	<form id="img-form" class="space-y-4" onsubmit={submit}>
		<Field label="Name" id="i-name" required error={errors.name}>
			<input id="i-name" class="input" bind:value={form.name} maxlength="80" required />
		</Field>
		<Field
			label="Reference"
			id="i-ref"
			required={!editing}
			error={errors.reference}
			hint={editing
				? 'The reference cannot be changed; add a new image instead.'
				: 'e.g. registry.example.org/org/image:1.2 or jenderal/python:3.12'}
		>
			<input
				id="i-ref"
				class="input font-mono"
				bind:value={form.reference}
				disabled={!!editing}
				required={!editing}
			/>
		</Field>
		<Field label="Description" id="i-desc" error={errors.description}>
			<textarea id="i-desc" class="input" rows="2" bind:value={form.description}></textarea>
		</Field>
		<div class="flex items-center gap-3">
			<Switch id="i-gpu" label="GPU capable" bind:checked={form.gpu_capable} />
			<label for="i-gpu" class="text-sm">GPU capable (contains CUDA libraries)</label>
		</div>
		<div class="flex items-center gap-3">
			<Switch id="i-allowed" label="Allowed" bind:checked={form.is_allowed} />
			<label for="i-allowed" class="text-sm">Allowed for templates</label>
		</div>
	</form>
	{#snippet footer()}
		<Button onclick={() => (open = false)}>Cancel</Button>
		<Button type="submit" form="img-form" variant="primary" loading={saving}
			>{editing ? 'Save image' : 'Add image'}</Button
		>
	{/snippet}
</Modal>
