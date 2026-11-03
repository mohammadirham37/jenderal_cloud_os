<script lang="ts">
	import { Check, CircleCheck, X } from '@lucide/svelte';
	import {
		Alert,
		Badge,
		Button,
		Card,
		EmptyState,
		ErrorState,
		Field,
		Modal,
		Skeleton
	} from '#lib/components/ui/index.ts';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import type { Resource } from '#lib/api/resource.svelte.ts';
	import type { GPUDevice, GPURequest } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
	import { bytes, dateTime, relative, titleCase } from '#lib/format.ts';

	let {
		requests,
		devices = [],
		onchange
	}: { requests: Resource<GPURequest[]>; devices?: GPUDevice[]; onchange?: () => void } = $props();

	const meId = $derived(auth.me?.id);
	const selectable = $derived(
		devices.filter((d) => d.status === 'available' && d.allocation_policy !== 'disabled')
	);

	let target = $state<GPURequest | null>(null);
	let open = $state(false);
	let deviceId = $state('');
	let hours = $state(1);
	let note = $state('');
	let saving = $state(false);
	let errors = $state<Record<string, string>>({});
	let formError = $state('');
	let busy = $state<string | null>(null);

	function isOwn(r: GPURequest) {
		return r.requester_id === meId && !auth.can('gpu.manage');
	}

	function openApprove(r: GPURequest) {
		target = r;
		deviceId = r.gpu_device_id ?? '';
		hours = r.duration_hours;
		note = '';
		errors = {};
		formError = '';
		open = true;
	}

	async function approve(e: SubmitEvent) {
		e.preventDefault();
		if (!target) return;
		saving = true;
		errors = {};
		formError = '';
		try {
			const body: Record<string, unknown> = { note: note.trim() };
			if (deviceId) body.gpu_device_id = deviceId;
			if (Number(hours) !== target.duration_hours) body.duration_hours = Number(hours);
			await api.post(`/gpu/requests/${target.id}/approve`, body);
			toast.success(
				'Request approved',
				`GPU allocated to ${target.workspace_name}. A running workspace restarts to attach it.`
			);
			open = false;
			await requests.reload();
			onchange?.();
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

	async function reject(r: GPURequest) {
		const { confirmed, reason } = await confirmDialog.ask({
			title: `Reject request from ${r.requester_username}?`,
			message: 'The requester is notified with your reason.',
			reasonLabel: 'Reason for rejection',
			reasonRequired: true,
			confirmLabel: 'Reject request',
			danger: true
		});
		if (!confirmed) return;
		busy = r.id;
		try {
			await api.post(`/gpu/requests/${r.id}/reject`, { note: reason });
			toast.success('Request rejected');
			await requests.reload();
			onchange?.();
		} catch (err) {
			toast.error('Could not reject the request', errorMessage(err));
		} finally {
			busy = null;
		}
	}
</script>

{#if requests.error}
	<Card><ErrorState error={requests.error} retry={requests.reload} /></Card>
{:else if !requests.data}
	<Card><Skeleton lines={4} /></Card>
{:else if requests.data.length === 0}
	<Card>
		<EmptyState
			icon={CircleCheck}
			title="No pending requests"
			description="New GPU requests appear here and approvers are notified."
		/>
	</Card>
{:else}
	<ul class="space-y-3">
		{#each requests.data as r (r.id)}
			<li class="card p-4">
				<div class="flex flex-wrap items-start justify-between gap-3">
					<div class="min-w-0">
						<p class="text-sm font-semibold">
							{r.requester_username}
							<span class="font-normal text-fg-muted">requests a GPU for</span>
							<a href="/workspaces/{r.workspace_id}" class="link">{r.workspace_name}</a>
						</p>
						<div class="mt-1 flex flex-wrap items-center gap-2 text-xs text-fg-muted">
							<Badge tone={r.mode === 'shared' ? 'accent' : 'info'}>{titleCase(r.mode)}</Badge>
							<span>{r.duration_hours} hours</span>
							<span>· GPU: {r.gpu_name ?? 'any'}</span>
							<span title={dateTime(r.created_at)}>· {relative(r.created_at)}</span>
						</div>
					</div>
					<div class="flex items-center gap-2">
						<Button
							size="sm"
							onclick={() => reject(r)}
							loading={busy === r.id}
							disabled={busy !== null}><X class="size-3.5" /> Reject</Button
						>
						<Button
							size="sm"
							variant="primary"
							onclick={() => openApprove(r)}
							disabled={busy !== null || isOwn(r)}
							title={isOwn(r) ? 'You cannot approve your own request' : undefined}
							><Check class="size-3.5" /> Approve</Button
						>
					</div>
				</div>
				<p class="mt-3 rounded-md bg-panel-2 px-3 py-2 text-sm whitespace-pre-line">{r.reason}</p>
				{#if isOwn(r)}
					<p class="mt-2 text-xs text-fg-muted">
						This is your own request; another approver must review it.
					</p>
				{/if}
			</li>
		{/each}
	</ul>
{/if}

<Modal
	bind:open
	title="Approve GPU request"
	description={target ? `${target.requester_username} · ${target.workspace_name}` : undefined}
>
	{#if target}
		<form id="gpu-approve" class="space-y-4" onsubmit={approve}>
			{#if formError}<Alert tone="danger">{formError}</Alert>{/if}
			<Field
				label="GPU"
				id="approve-device"
				error={errors.gpu_device_id}
				hint="Leave on automatic to pick the first GPU that fits the requested mode."
			>
				<select id="approve-device" class="input" bind:value={deviceId}>
					<option value="">Automatic</option>
					{#each selectable as d (d.id)}
						<option value={d.id}
							>#{d.index}
							{d.name}{d.memory_total_bytes ? ` · ${bytes(d.memory_total_bytes)}` : ''} ·
							{d.allocation_policy} · {d.active_allocations} active</option
						>
					{/each}
				</select>
			</Field>
			<Field
				label="Duration (hours)"
				id="approve-hours"
				error={errors.duration_hours}
				hint="You may shorten the requested {target.duration_hours} hours."
			>
				<input
					id="approve-hours"
					type="number"
					class="input w-32"
					min="1"
					max={target.duration_hours}
					bind:value={hours}
				/>
			</Field>
			<Field label="Note to requester" id="approve-note" error={errors.note} hint="Optional.">
				<textarea id="approve-note" class="input" rows="2" bind:value={note}></textarea>
			</Field>
			<Alert tone="info">If the workspace is running it restarts to attach the GPU.</Alert>
		</form>
	{/if}
	{#snippet footer()}
		<Button onclick={() => (open = false)}>Cancel</Button>
		<Button
			type="submit"
			form="gpu-approve"
			variant="primary"
			loading={saving}
			disabled={!target || hours < 1 || hours > target.duration_hours}>Approve</Button
		>
	{/snippet}
</Modal>
