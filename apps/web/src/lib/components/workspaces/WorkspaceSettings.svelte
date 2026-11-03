<script lang="ts">
	// Settings tab (requires Manage): rename, idle timeout and CPU/memory limits.
	import { untrack } from 'svelte';
	import { Save } from '@lucide/svelte';
	import { api, ApiError, errorMessage } from '#lib/api/client.ts';
	import type { WorkspaceDetail } from '#lib/api/types.ts';
	import { Alert, Button, Card, Field } from '#lib/components/ui/index.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { GiB, bytes, cores } from '#lib/format.ts';
	import { fromUnits, toUnits } from './workspace.ts';

	let { ws, onsaved }: { ws: WorkspaceDetail; onsaved: (d: WorkspaceDetail) => void } = $props();

	// Form state is initialised from the workspace once (and after each save),
	// so background polling does not overwrite what the user is typing.
	let name = $state('');
	let idle = $state<number | null>(0);
	let cpu = $state(1);
	let memory = $state(1);
	let errors = $state<Record<string, string>>({});
	let formError = $state('');
	let saving = $state(false);

	function load(d: WorkspaceDetail) {
		name = d.name;
		idle = d.idle_timeout_min ?? 0;
		cpu = d.cpu_millis / 1000;
		memory = fromUnits(d.memory_bytes, GiB);
	}
	untrack(() => load(ws));

	const running = $derived(ws.status === 'running');
	const tpl = $derived(ws.template);

	async function save(e: SubmitEvent) {
		e.preventDefault();
		errors = {};
		formError = '';
		const body: Record<string, unknown> = {};
		const v: Record<string, string> = {};
		if (name.trim() !== ws.name) {
			if (!name.trim() || name.trim().length > 80) v.name = 'Name is required (max 80 characters).';
			body.name = name.trim();
		}
		const idleVal = idle ?? 0;
		if (idleVal !== (ws.idle_timeout_min ?? 0)) {
			if (!Number.isInteger(idleVal) || (idleVal !== 0 && (idleVal < 5 || idleVal > 10080)))
				v.idle_timeout_min = 'Use 0 (disabled) or whole minutes between 5 and 10080.';
			body.idle_timeout_min = idleVal;
		}
		const cpuMillis = toUnits(cpu ?? 0, 1000);
		if (cpuMillis !== ws.cpu_millis) {
			if (!Number.isFinite(cpu) || cpuMillis < tpl.min_cpu_millis)
				v.cpu_millis = `At least ${cores(tpl.min_cpu_millis)} (template minimum).`;
			body.cpu_millis = cpuMillis;
		}
		const memBytes = toUnits(memory ?? 0, GiB);
		if (Math.abs(memBytes - ws.memory_bytes) > 1024 * 1024) {
			if (!Number.isFinite(memory) || memBytes < tpl.min_memory_bytes)
				v.memory_bytes = `At least ${bytes(tpl.min_memory_bytes)} (template minimum).`;
			body.memory_bytes = memBytes;
		}
		if (Object.keys(v).length) {
			errors = v;
			return;
		}
		if (Object.keys(body).length === 0) {
			toast.info('No changes to save');
			return;
		}
		saving = true;
		try {
			const d = await api.patch<WorkspaceDetail>(`/workspaces/${ws.id}`, body);
			toast.success(
				'Settings saved',
				'cpu_millis' in body || 'memory_bytes' in body
					? 'New CPU/memory limits apply the next time the workspace starts.'
					: undefined
			);
			load(d);
			onsaved(d);
		} catch (err) {
			if (err instanceof ApiError && err.status === 422 && Object.keys(err.details).length) {
				errors = { ...err.details };
			} else if (err instanceof ApiError && err.status === 409) {
				formError = `${err.message} New limits are applied on the next start.`;
			} else {
				formError = errorMessage(err);
			}
		} finally {
			saving = false;
		}
	}
</script>

<Card title="Settings" description="Changes are recorded in the audit log.">
	<form class="grid max-w-2xl gap-5 sm:grid-cols-2" novalidate onsubmit={save}>
		{#if formError}
			<div class="sm:col-span-2">
				<Alert tone="warning" title="Could not save">{formError}</Alert>
			</div>
		{/if}
		<div class="sm:col-span-2">
			<Field label="Name" id="set-name" error={errors.name} required>
				<input id="set-name" class="input" bind:value={name} maxlength="80" />
			</Field>
		</div>
		<div class="sm:col-span-2">
			<Field
				label="Idle timeout (minutes)"
				id="set-idle"
				error={errors.idle_timeout_min}
				hint="Stop automatically after this many minutes without activity. 0 disables the timeout."
			>
				<input
					id="set-idle"
					type="number"
					class="input"
					min="0"
					max="10080"
					step="1"
					bind:value={idle}
				/>
			</Field>
		</div>
		{#if running}
			<div class="sm:col-span-2">
				<Alert tone="info" title="CPU and memory are locked while running">
					Stop the workspace to change its CPU or memory limits. New limits apply on the next start.
				</Alert>
			</div>
		{/if}
		<Field
			label="CPU (cores)"
			id="set-cpu"
			error={errors.cpu_millis}
			hint="Min. {cores(tpl.min_cpu_millis)}; max. the owner's quota."
		>
			<input
				id="set-cpu"
				type="number"
				class="input"
				step="0.25"
				min={tpl.min_cpu_millis / 1000}
				bind:value={cpu}
				disabled={running}
			/>
		</Field>
		<Field
			label="Memory (GiB)"
			id="set-mem"
			error={errors.memory_bytes}
			hint="Min. {bytes(tpl.min_memory_bytes)}; max. the owner's quota."
		>
			<input
				id="set-mem"
				type="number"
				class="input"
				step="0.25"
				min={fromUnits(tpl.min_memory_bytes, GiB)}
				bind:value={memory}
				disabled={running}
			/>
		</Field>
		<p class="text-xs text-fg-muted sm:col-span-2">
			Storage size ({bytes(ws.storage_bytes)}) cannot be changed after creation.
		</p>
		<div class="sm:col-span-2">
			<Button type="submit" variant="primary" loading={saving}><Save class="size-4" /> Save</Button>
		</div>
	</form>
</Card>
