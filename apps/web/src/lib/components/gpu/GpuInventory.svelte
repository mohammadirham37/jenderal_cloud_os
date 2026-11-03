<script lang="ts">
	import { Cpu, Pencil } from '@lucide/svelte';
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
		Skeleton,
		StatusBadge
	} from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import type { Resource } from '#lib/api/resource.svelte.ts';
	import type { GPUDevice } from '#lib/api/types.ts';
	import type { GPUInventory } from '#lib/api/types-gpu.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes, percent, relative, titleCase } from '#lib/format.ts';

	let { inventory }: { inventory: Resource<GPUInventory | null> } = $props();

	const inv = $derived(inventory.data);
	const canManage = $derived(auth.can('gpu.manage'));

	// ---- Policy editing
	let editing = $state<GPUDevice | null>(null);
	let editOpen = $state(false);
	let policy = $state<GPUDevice['allocation_policy']>('exclusive');
	let maxShared = $state(1);
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let formError = $state('');

	function edit(d: GPUDevice) {
		editing = d;
		policy = d.allocation_policy;
		maxShared = d.max_shared;
		errors = {};
		formError = '';
		editOpen = true;
	}

	async function save(e: SubmitEvent) {
		e.preventDefault();
		if (!editing || !inv) return;
		saving = true;
		errors = {};
		formError = '';
		try {
			const body: Record<string, unknown> = { allocation_policy: policy };
			if (policy === 'shared') body.max_shared = Number(maxShared);
			const updated = await api.patch<GPUDevice>(`/gpu/devices/${editing.id}`, body);
			inventory.set({
				...inv,
				devices: inv.devices.map((d) => (d.id === updated.id ? updated : d))
			});
			editOpen = false;
			toast.success('GPU policy updated', `${updated.name} (#${updated.index})`);
		} catch (err) {
			if (err instanceof ApiError && err.status === 422 && Object.keys(err.details).length) {
				errors = err.details;
			} else {
				formError = errorMessage(err);
			}
		} finally {
			saving = false;
		}
	}

	const policyTone = { exclusive: 'info', shared: 'accent', disabled: 'neutral' } as const;
</script>

{#if inventory.error}
	<Card><ErrorState error={inventory.error} retry={inventory.reload} /></Card>
{:else if !inv}
	<div class="grid gap-4 md:grid-cols-2">
		{#each [1, 2] as i (i)}<div class="card p-5"><Skeleton lines={5} /></div>{/each}
	</div>
{:else if !inv.gpu_enabled}
	<Card>
		<EmptyState
			icon={Cpu}
			title="GPU support is disabled"
			description="GPU support is not enabled on this server. An administrator can enable it in the server configuration once an NVIDIA GPU, driver and container runtime are installed."
		/>
	</Card>
{:else}
	<div class="space-y-3">
		{#if inv.inventory_error}
			<Alert tone="warning" title="Live GPU information unavailable">{inv.inventory_error}</Alert>
		{/if}
		{#if inv.nvidia_driver && !inv.nvidia_container_runtime}
			<Alert tone="warning" title="NVIDIA container runtime not detected">
				The NVIDIA driver is installed but the NVIDIA container runtime is missing, so GPUs cannot
				be attached to workspace containers until it is installed.
			</Alert>
		{/if}
	</div>

	{#if inv.devices.length === 0}
		<Card class={inv.inventory_error || !inv.nvidia_container_runtime ? 'mt-4' : ''}>
			{#if !inv.nvidia_driver}
				<EmptyState
					icon={Cpu}
					title="NVIDIA driver not detected"
					description="The host agent did not find an NVIDIA driver (nvidia-smi). Install the NVIDIA driver on the host to make GPUs available."
				/>
			{:else}
				<EmptyState
					icon={Cpu}
					title="No NVIDIA GPU detected"
					description="The driver is present but no GPU has been reported by the host agent yet. Inventory is refreshed periodically."
				/>
			{/if}
		</Card>
	{:else}
		<div class="mt-4 grid gap-4 lg:grid-cols-2">
			{#each inv.devices as d (d.id)}
				{@const procs = inv.processes?.[d.uuid] ?? null}
				<Card>
					<div class="flex flex-wrap items-start justify-between gap-3">
						<div class="min-w-0">
							<h3 class="truncate text-sm font-semibold">
								<span class="text-fg-muted">#{d.index}</span>
								{d.name}
							</h3>
							<p class="mt-0.5 truncate font-mono text-xs text-fg-subtle" title={d.uuid}>
								{d.uuid}
							</p>
						</div>
						<div class="flex items-center gap-2">
							<StatusBadge status={d.status} />
							{#if canManage}
								<Button size="sm" variant="ghost" onclick={() => edit(d)} aria-label="Edit policy"
									><Pencil class="size-3.5" /> Policy</Button
								>
							{/if}
						</div>
					</div>

					<div class="mt-4 space-y-3">
						<Meter
							label="VRAM"
							value={d.memory_used_bytes}
							max={d.memory_total_bytes ?? 0}
							display="{bytes(d.memory_used_bytes)} / {bytes(d.memory_total_bytes)}"
						/>
						<Meter
							label="Utilization"
							value={d.utilization_pct}
							max={100}
							display={percent(d.utilization_pct)}
						/>
					</div>

					<dl class="mt-4 grid grid-cols-2 gap-x-6 gap-y-1.5 text-sm">
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Driver</dt>
							<dd>{d.driver_version ?? '—'}</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">CUDA</dt>
							<dd>{d.cuda_version ?? '—'}</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Temperature</dt>
							<dd class="tabular-nums">
								{d.temperature_c === null ? '—' : `${d.temperature_c} °C`}
							</dd>
						</div>
						{#if d.mig_mode}
							<div class="flex justify-between gap-2">
								<dt class="text-fg-muted">MIG mode</dt>
								<dd>{d.mig_mode}</dd>
							</div>
						{/if}
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Policy</dt>
							<dd>
								<Badge tone={policyTone[d.allocation_policy]}
									>{titleCase(d.allocation_policy)}{d.allocation_policy === 'shared'
										? ` · max ${d.max_shared}`
										: ''}</Badge
								>
							</dd>
						</div>
						<div class="flex justify-between gap-2">
							<dt class="text-fg-muted">Active allocations</dt>
							<dd class="tabular-nums">{d.active_allocations}</dd>
						</div>
						<div class="col-span-2 flex justify-between gap-2">
							<dt class="text-fg-muted">Last seen</dt>
							<dd>{relative(d.last_seen_at)}</dd>
						</div>
					</dl>

					{#if procs && procs.length > 0}
						<div class="mt-4 border-t border-border pt-3">
							<h4 class="mb-2 text-xs font-medium tracking-wide text-fg-muted uppercase">
								Processes
							</h4>
							<div class="overflow-x-auto">
								<table class="w-full text-left text-xs">
									<thead class="text-fg-muted">
										<tr>
											<th scope="col" class="py-1 pr-3 font-medium">PID</th>
											<th scope="col" class="py-1 pr-3 font-medium">Process</th>
											<th scope="col" class="py-1 pr-3 text-right font-medium">VRAM</th>
											<th scope="col" class="py-1 font-medium">Container</th>
										</tr>
									</thead>
									<tbody class="divide-y divide-border">
										{#each procs as pr (pr.pid)}
											<tr>
												<td class="py-1 pr-3 font-mono tabular-nums">{pr.pid}</td>
												<td class="max-w-48 truncate py-1 pr-3 font-mono" title={pr.name}
													>{pr.name}</td
												>
												<td class="py-1 pr-3 text-right tabular-nums">{bytes(pr.memory_bytes)}</td>
												<td class="py-1 font-mono text-fg-muted">
													{pr.container_id ? pr.container_id.slice(0, 8) : 'Not managed by ITK'}
												</td>
											</tr>
										{/each}
									</tbody>
								</table>
							</div>
						</div>
					{:else if inv.processes}
						<p class="mt-4 border-t border-border pt-3 text-xs text-fg-muted">
							No compute processes running on this GPU.
						</p>
					{/if}
				</Card>
			{/each}
		</div>
	{/if}
{/if}

<Modal
	bind:open={editOpen}
	title="Allocation policy"
	description={editing ? `#${editing.index} ${editing.name}` : undefined}
>
	<form id="gpu-policy" class="space-y-4" onsubmit={save}>
		{#if formError}<Alert tone="danger">{formError}</Alert>{/if}
		<Field label="Policy" id="gpu-policy-select" error={errors.allocation_policy}>
			<select id="gpu-policy-select" class="input" bind:value={policy}>
				<option value="exclusive">Exclusive — one workspace at a time</option>
				<option value="shared">Shared — several workspaces share the GPU</option>
				<option value="disabled">Disabled — not allocatable</option>
			</select>
		</Field>
		{#if policy === 'shared'}
			<Field
				label="Maximum shared allocations"
				id="gpu-max-shared"
				error={errors.max_shared}
				hint="1–16. VRAM is not partitioned; workspaces compete for memory."
			>
				<input
					id="gpu-max-shared"
					type="number"
					class="input w-32"
					min="1"
					max="16"
					bind:value={maxShared}
				/>
			</Field>
		{/if}
		<p class="text-xs text-fg-muted">
			Changes apply to new allocations. Existing allocations are not revoked.
		</p>
	</form>
	{#snippet footer()}
		<Button onclick={() => (editOpen = false)}>Cancel</Button>
		<Button type="submit" form="gpu-policy" variant="primary" loading={saving}>Save</Button>
	{/snippet}
</Modal>
