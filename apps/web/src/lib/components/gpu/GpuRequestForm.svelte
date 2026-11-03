<script lang="ts">
	import { Layers, Send } from '@lucide/svelte';
	import {
		Alert,
		Button,
		Card,
		EmptyState,
		ErrorState,
		Field,
		Skeleton
	} from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { GPUDevice, GPURequest, Quota, Usage, Workspace } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { bytes, titleCase } from '#lib/format.ts';

	let {
		devices = [],
		gpuEnabled = true,
		onsubmitted
	}: {
		devices?: GPUDevice[];
		gpuEnabled?: boolean;
		onsubmitted?: (r: GPURequest) => void;
	} = $props();

	const workspaces = resource(() =>
		api.list<Workspace>('/workspaces', { mine: 'true', per_page: 100, sort: 'name' })
	);
	const usage = resource(() => api.get<{ quota: Quota; usage: Usage }>('/me/usage'));

	const eligible = $derived(
		(workspaces.data?.data ?? []).filter(
			(w) => w.type === 'container' && w.status !== 'deleting' && w.status !== 'failed'
		)
	);
	const selectable = $derived(
		devices.filter((d) => d.status === 'available' && d.allocation_policy !== 'disabled')
	);
	const gpuAllowed = $derived(usage.data?.quota.gpu_allowed);

	let workspaceId = $state('');
	let deviceId = $state('');
	let mode = $state<'exclusive' | 'shared'>('exclusive');
	let hours = $state(4);
	let reason = $state('');
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let formError = $state('');

	$effect(() => {
		if (!workspaceId && eligible.length > 0) workspaceId = eligible[0].id;
	});

	const reasonLength = $derived(reason.trim().length);

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		saving = true;
		errors = {};
		formError = '';
		try {
			const body: Record<string, unknown> = {
				workspace_id: workspaceId,
				mode,
				duration_hours: Number(hours),
				reason: reason.trim()
			};
			if (deviceId) body.gpu_device_id = deviceId;
			const r = await api.post<GPURequest>('/gpu/requests', body);
			toast.success('GPU request submitted', 'An approver has been notified.');
			reason = '';
			onsubmitted?.(r);
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
</script>

<div class="grid gap-6 lg:grid-cols-3">
	<Card
		title="Request GPU access"
		description="Ask for a GPU to be attached to one of your container workspaces."
		class="lg:col-span-2"
	>
		{#if !auth.can('gpu.request')}
			<Alert tone="info">Your role does not allow requesting GPU access.</Alert>
		{:else if !gpuEnabled}
			<Alert tone="info" title="GPU support is disabled">
				GPU requests cannot be submitted because GPU support is not enabled on this server.
			</Alert>
		{:else if workspaces.error}
			<ErrorState error={workspaces.error} retry={workspaces.reload} />
		{:else if !workspaces.data}
			<Skeleton lines={5} />
		{:else if eligible.length === 0}
			<EmptyState
				icon={Layers}
				title="No container workspace"
				description="GPUs can only be attached to container workspaces that you own. Create one first."
			>
				{#snippet action()}
					{#if auth.can('workspaces.create')}
						<Button href="/workspaces/new" variant="primary">Create workspace</Button>
					{/if}
				{/snippet}
			</EmptyState>
		{:else}
			{#if gpuAllowed === false}
				<div class="mb-4">
					<Alert tone="warning" title="Your quota does not include GPU access">
						Your quota package ({usage.data?.quota.name}) does not allow GPUs, so the request will
						be refused. Ask an administrator for a package that allows GPU access.
					</Alert>
				</div>
			{/if}
			<form class="space-y-4" onsubmit={submit}>
				{#if formError}<Alert tone="danger">{formError}</Alert>{/if}
				<Field label="Workspace" id="gpu-ws" error={errors.workspace_id} required>
					<select id="gpu-ws" class="input" bind:value={workspaceId} required>
						{#each eligible as w (w.id)}
							<option value={w.id}>{w.name} ({titleCase(w.status)})</option>
						{/each}
					</select>
				</Field>
				<div class="grid gap-4 sm:grid-cols-2">
					<Field
						label="GPU"
						id="gpu-device"
						error={errors.gpu_device_id}
						hint="Optional. The approver may choose a different GPU."
					>
						<select id="gpu-device" class="input" bind:value={deviceId}>
							<option value="">Any available GPU</option>
							{#each selectable as d (d.id)}
								<option value={d.id}
									>#{d.index}
									{d.name}{d.memory_total_bytes ? ` · ${bytes(d.memory_total_bytes)}` : ''} · {d.allocation_policy}</option
								>
							{/each}
						</select>
					</Field>
					<Field
						label="Mode"
						id="gpu-mode"
						error={errors.mode}
						hint="Shared mode is only possible when enabled by the administrator."
					>
						<select id="gpu-mode" class="input" bind:value={mode}>
							<option value="exclusive">Exclusive (whole GPU)</option>
							<option value="shared">Shared (VRAM is not partitioned)</option>
						</select>
					</Field>
				</div>
				<Field
					label="Duration (hours)"
					id="gpu-hours"
					error={errors.duration_hours}
					hint="The allocation is released automatically when it ends."
					required
				>
					<input
						id="gpu-hours"
						type="number"
						class="input w-32"
						min="1"
						bind:value={hours}
						required
					/>
				</Field>
				<Field
					label="Reason"
					id="gpu-reason"
					error={errors.reason}
					hint="{reasonLength}/2000 · at least 10 characters. Describe the workload (e.g. model training for a thesis)."
					required
				>
					<textarea
						id="gpu-reason"
						class="input"
						rows="4"
						maxlength="2000"
						bind:value={reason}
						required></textarea>
				</Field>
				<div class="flex justify-end">
					<Button
						type="submit"
						variant="primary"
						loading={saving}
						disabled={!workspaceId || reasonLength < 10 || hours < 1}
						><Send class="size-4" /> Submit request</Button
					>
				</div>
			</form>
		{/if}
	</Card>

	<Card title="How it works">
		<ol class="list-decimal space-y-2 pl-5 text-sm text-fg-muted">
			<li>Submit a request for one of your container workspaces.</li>
			<li>
				An approver reviews it. They may pick a specific GPU or shorten the duration. You are
				notified of the decision.
			</li>
			<li>
				Once approved, a running workspace <strong class="text-fg">restarts</strong> to attach the GPU.
				Save your work before the request is reviewed.
			</li>
			<li>
				When the allocation ends or is released, the workspace restarts again to detach the GPU.
			</li>
		</ol>
		<p class="mt-4 border-t border-border pt-4 text-sm text-fg-muted">
			Your quota package must allow GPUs:
			<strong class="text-fg"
				>{gpuAllowed === undefined ? 'checking…' : gpuAllowed ? 'allowed' : 'not allowed'}</strong
			>.
		</p>
	</Card>
</div>
