<script lang="ts">
	// Adds an extra container to a container workspace (POST /containers).
	import { untrack } from 'svelte';
	import { api, ApiError, errorMessage, idempotencyKey } from '#lib/api/client.ts';
	import type { Container, Template, Workspace } from '#lib/api/types.ts';
	import { Alert, Button, Field, Modal, Skeleton } from '#lib/components/ui/index.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { GiB, bytes, cores } from '#lib/format.ts';
	import { fromUnits, toUnits, withAccessReason } from './workspace.ts';

	let {
		open = $bindable(false),
		workspace,
		oncreated
	}: { open?: boolean; workspace: Workspace; oncreated: (c: Container) => void } = $props();

	let templates = $state<Template[] | null>(null);
	let loadError = $state('');
	let templateId = $state('');
	let name = $state('');
	let cpu = $state(0.5);
	let memory = $state(0.5);
	let errors = $state<Record<string, string>>({});
	let formError = $state('');
	let submitting = $state(false);
	let key = idempotencyKey();

	const tpl = $derived(templates?.find((t) => t.id === templateId));

	function reset() {
		errors = {};
		formError = '';
		name = '';
		key = idempotencyKey();
		if (templates) return;
		api
			.get<Template[]>('/templates')
			.then((list) => {
				templates = list;
				if (list.length) pick(list[0].id);
			})
			.catch((e) => (loadError = errorMessage(e)));
	}

	$effect(() => {
		if (open) untrack(reset);
	});

	function pick(id: string) {
		templateId = id;
		const t = templates?.find((x) => x.id === id);
		if (t) {
			cpu = t.rec_cpu_millis / 1000;
			memory = fromUnits(t.rec_memory_bytes, GiB);
		}
	}

	function validate(): boolean {
		const e: Record<string, string> = {};
		if (!tpl) e.template_id = 'Choose a template.';
		if (!/^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$/.test(name))
			e.name = 'Use 2–40 lowercase letters, digits or "-" (not at the start or end).';
		if (tpl && (!Number.isFinite(cpu) || toUnits(cpu, 1000) < tpl.min_cpu_millis))
			e.cpu_millis = `At least ${cores(tpl.min_cpu_millis)}.`;
		if (tpl && (!Number.isFinite(memory) || toUnits(memory, GiB) < tpl.min_memory_bytes))
			e.memory_bytes = `At least ${bytes(tpl.min_memory_bytes)}.`;
		errors = e;
		return Object.keys(e).length === 0;
	}

	async function submit(ev: SubmitEvent) {
		ev.preventDefault();
		formError = '';
		if (!validate()) return;
		submitting = true;
		try {
			const c = await withAccessReason((headers) =>
				api.post<Container>(
					'/containers',
					{
						workspace_id: workspace.id,
						template_id: templateId,
						name,
						cpu_millis: toUnits(cpu, 1000),
						memory_bytes: toUnits(memory, GiB)
					},
					{ headers: { ...headers, 'Idempotency-Key': key } }
				)
			);
			if (!c) return;
			toast.success('Container added', `${c.name} is being created.`);
			open = false;
			oncreated(c);
		} catch (e) {
			if (e instanceof ApiError && e.status === 422 && Object.keys(e.details).length) {
				errors = { ...e.details };
			} else {
				formError = errorMessage(e);
			}
		} finally {
			submitting = false;
		}
	}
</script>

<Modal
	bind:open
	title="Add container"
	description="The container shares the workspace volume and network."
>
	<form id="add-container-form" class="space-y-4" novalidate onsubmit={submit}>
		{#if formError}<Alert tone="danger" title="Could not add the container">{formError}</Alert>{/if}
		{#if loadError}
			<Alert tone="danger" title="Templates could not be loaded">{loadError}</Alert>
		{:else if !templates}
			<Skeleton lines={3} />
		{:else}
			<Field label="Template" id="ac-template" error={errors.template_id} required>
				<select
					id="ac-template"
					class="input"
					value={templateId}
					onchange={(e) => pick(e.currentTarget.value)}
				>
					{#each templates as t (t.id)}<option value={t.id}>{t.name} ({t.category})</option>{/each}
				</select>
			</Field>
			<Field
				label="Name"
				id="ac-name"
				error={errors.name}
				required
				hint="Lowercase letters, digits and dashes, e.g. “db” or “worker-1”."
			>
				<input
					id="ac-name"
					class="input font-mono"
					bind:value={() => name, (v) => (name = v.toLowerCase())}
					maxlength="40"
					autocomplete="off"
					aria-invalid={!!errors.name}
				/>
			</Field>
			<div class="grid gap-4 sm:grid-cols-2">
				<Field
					label="CPU (cores)"
					id="ac-cpu"
					error={errors.cpu_millis}
					hint={tpl ? `Min. ${cores(tpl.min_cpu_millis)}` : undefined}
				>
					<input
						id="ac-cpu"
						type="number"
						class="input"
						step="0.25"
						min={tpl ? tpl.min_cpu_millis / 1000 : 0.25}
						bind:value={cpu}
					/>
				</Field>
				<Field
					label="Memory (GiB)"
					id="ac-mem"
					error={errors.memory_bytes}
					hint={tpl ? `Min. ${bytes(tpl.min_memory_bytes)}` : undefined}
				>
					<input
						id="ac-mem"
						type="number"
						class="input"
						step="0.25"
						min={tpl ? fromUnits(tpl.min_memory_bytes, GiB) : 0.25}
						bind:value={memory}
					/>
				</Field>
			</div>
			<p class="text-xs text-fg-muted">
				The container counts towards your running CPU/memory quota while it runs.
			</p>
		{/if}
	</form>
	{#snippet footer()}
		<Button onclick={() => (open = false)}>Cancel</Button>
		<Button
			type="submit"
			form="add-container-form"
			variant="primary"
			loading={submitting}
			disabled={!templates}>Add container</Button
		>
	{/snippet}
</Modal>
