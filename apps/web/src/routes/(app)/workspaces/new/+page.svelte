<script lang="ts">
	import { ArrowLeft, ArrowRight, Check, Rocket } from '@lucide/svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { api, ApiError, errorMessage, idempotencyKey } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Project, Template, Workspace } from '#lib/api/types.ts';
	import type { MyUsage } from '#lib/api/types-workspaces.ts';
	import {
		Alert,
		Button,
		Card,
		EmptyState,
		ErrorState,
		Field,
		PageHeader,
		Skeleton
	} from '#lib/components/ui/index.ts';
	import TemplateCard from '#lib/components/workspaces/TemplateCard.svelte';
	import { fromUnits, idleLabel, toUnits } from '#lib/components/workspaces/workspace.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { appConfig } from '#lib/stores/config.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { GiB, bytes, cores, titleCase } from '#lib/format.ts';

	const templates = resource(() => api.get<Template[]>('/templates'));
	const usage = resource(() => api.get<MyUsage>('/me/usage'));
	const projects = resource(() =>
		api.list<Project>('/projects', { mine: 'true', status: 'active', per_page: 100 })
	);

	// One key per wizard: retries of the same submission are de-duplicated server-side.
	const idemKey = idempotencyKey();

	const steps = ['Template', 'Configure', 'Review'];
	let step = $state(1);
	let templateId = $state('');
	let name = $state('');
	let type = $state<'container' | 'native'>('container');
	let cpu = $state(1);
	let memory = $state(1);
	let storage = $state(5);
	let idle = $state<number | null>(null);
	let projectId = $state('');
	let errors = $state<Record<string, string>>({});
	let formError = $state<{ title: string; message: string } | null>(null);
	let submitting = $state(false);

	const quota = $derived(usage.data?.quota);
	const used = $derived(usage.data?.usage);
	const tpl = $derived(templates.data?.find((t) => t.id === templateId));
	const nativeAllowed = $derived(
		!!appConfig.value?.native_workspaces && auth.can('workspaces.native')
	);
	const remainingStorage = $derived(
		quota && used ? Math.max(0, quota.storage_bytes - used.allocated_storage_bytes) : 0
	);
	const remainingWorkspaces = $derived(
		quota && used ? Math.max(0, quota.max_workspaces - used.workspaces) : 0
	);
	const projectChoices = $derived(
		(projects.data?.data ?? []).filter((p) => p.my_role !== null && p.my_role !== 'viewer')
	);
	const grouped = $derived.by(() => {
		const map: Record<string, Template[]> = {};
		for (const t of templates.data ?? []) {
			(map[t.category] ??= []).push(t);
		}
		return Object.entries(map);
	});

	// Bounds in user-facing units (cores / GiB).
	const bounds = $derived({
		cpuMin: (tpl?.min_cpu_millis ?? 250) / 1000,
		cpuMax: (quota?.cpu_millis ?? 0) / 1000,
		memMin: fromUnits(tpl?.min_memory_bytes ?? 0, GiB),
		memMax: fromUnits(quota?.memory_bytes ?? 0, GiB),
		storageMin: 0.25,
		storageMax: Math.floor((remainingStorage / GiB) * 4) / 4
	});

	/** Reason a template cannot be used with the current quota, if any. */
	function unusable(t: Template): string | undefined {
		if (!t.image_present && !nativeAllowed)
			return 'Not installed on this server yet — ask an administrator to build this image.';
		if (!quota || !used) return undefined;
		if (t.gpu_mode === 'required' && !quota.gpu_allowed)
			return 'Requires GPU access, which your quota package does not include.';
		if (t.min_cpu_millis > quota.cpu_millis)
			return `Needs at least ${cores(t.min_cpu_millis)}; your quota allows ${cores(quota.cpu_millis)}.`;
		if (t.min_memory_bytes > quota.memory_bytes)
			return `Needs at least ${bytes(t.min_memory_bytes)} memory; your quota allows ${bytes(quota.memory_bytes)}.`;
		return undefined;
	}

	const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

	function selectTemplate(t: Template) {
		templateId = t.id;
		errors = {};
		if (!quota) return;
		cpu = clamp(t.rec_cpu_millis, t.min_cpu_millis, quota.cpu_millis) / 1000;
		memory = fromUnits(clamp(t.rec_memory_bytes, t.min_memory_bytes, quota.memory_bytes), GiB);
		const st = clamp(t.default_storage_bytes, 256 * 1024 ** 2, Math.max(remainingStorage, 0));
		storage = fromUnits(st, GiB);
	}

	// Preselect ?template=<key> once templates and quota are loaded.
	let preselected = false;
	$effect(() => {
		if (preselected || !templates.data || !usage.data) return;
		preselected = true;
		// ?project_id=<id> comes from a project's "New workspace" link.
		const pid = page.url.searchParams.get('project_id');
		if (pid) projectId = pid;
		const key = page.url.searchParams.get('template');
		const t = key ? templates.data.find((x) => x.key === key) : undefined;
		if (t && !unusable(t)) {
			selectTemplate(t);
			if (!name) name = t.name;
			step = 2;
		} else if (key && !t) {
			toast.warning('Template not found', `The template "${key}" is not available.`);
		}
	});

	function validateConfig(): boolean {
		const e: Record<string, string> = {};
		const b = bounds;
		if (!name.trim()) e.name = 'Name is required.';
		else if (name.trim().length > 80) e.name = 'Use at most 80 characters.';
		if (!Number.isFinite(cpu) || cpu < b.cpuMin || cpu > b.cpuMax)
			e.cpu_millis = `Choose between ${b.cpuMin} and ${b.cpuMax} cores.`;
		if (!Number.isFinite(memory) || memory < b.memMin || memory > b.memMax)
			e.memory_bytes = `Choose between ${b.memMin} and ${b.memMax} GiB.`;
		if (!Number.isFinite(storage) || storage < b.storageMin)
			e.storage_bytes = 'Storage must be at least 0.25 GiB (256 MiB).';
		else if (storage > b.storageMax)
			e.storage_bytes = `Only ${bytes(remainingStorage)} of storage quota is left.`;
		if (idle !== null && (idle < 5 || idle > 10080 || !Number.isInteger(idle)))
			e.idle_timeout_min = 'Enter whole minutes between 5 and 10080, or leave empty.';
		errors = e;
		return Object.keys(e).length === 0;
	}

	function next() {
		formError = null;
		if (step === 1) {
			if (!tpl) {
				errors = { template_id: 'Choose a template to continue.' };
				return;
			}
			if (!name) name = tpl.name;
			step = 2;
		} else if (step === 2 && validateConfig()) {
			step = 3;
		}
	}

	const step1Fields = ['template_id'];
	const step2Fields = [
		'name',
		'type',
		'cpu_millis',
		'memory_bytes',
		'storage_bytes',
		'idle_timeout_min'
	];

	async function submit() {
		if (!tpl || !validateConfig()) {
			step = tpl ? 2 : 1;
			return;
		}
		submitting = true;
		formError = null;
		try {
			const ws = await api.post<Workspace>(
				'/workspaces',
				{
					name: name.trim(),
					template_id: tpl.id,
					type,
					cpu_millis: toUnits(cpu, 1000),
					memory_bytes: toUnits(memory, GiB),
					storage_bytes: toUnits(storage, GiB),
					idle_timeout_min: idle ?? undefined,
					project_id: projectId || undefined
				},
				{ headers: { 'Idempotency-Key': idemKey } }
			);
			toast.success('Workspace requested', `${ws.name} is being provisioned.`);
			await goto(`/workspaces/${ws.id}`);
		} catch (e) {
			if (e instanceof ApiError && e.status === 422 && Object.keys(e.details).length) {
				errors = { ...e.details };
				const keys = Object.keys(e.details);
				if (keys.some((k) => step1Fields.includes(k))) step = 1;
				else if (keys.some((k) => step2Fields.includes(k))) step = 2;
				formError = { title: 'Please correct the highlighted fields', message: e.message };
			} else if (e instanceof ApiError && e.code === 'quota_exceeded') {
				formError = { title: 'Quota exceeded', message: e.message };
			} else if (e instanceof ApiError && e.status === 403) {
				formError = { title: 'Not allowed', message: e.message };
			} else {
				formError = { title: 'Could not create the workspace', message: errorMessage(e) };
			}
		} finally {
			submitting = false;
		}
	}

	const loadError = $derived(templates.error ?? usage.error);
	const selectedProject = $derived(projectChoices.find((p) => p.id === projectId));
</script>

<PageHeader
	title="New workspace"
	description="Create a development environment from a template in three steps."
>
	{#snippet actions()}
		<Button href="/workspaces" variant="ghost"><ArrowLeft class="size-4" /> Back</Button>
	{/snippet}
</PageHeader>

{#if !auth.can('workspaces.create')}
	<Alert tone="warning" title="You cannot create workspaces">
		Your role does not include the permission to create workspaces. Ask an administrator for access.
	</Alert>
{:else if loadError}
	<Card
		><ErrorState
			error={loadError}
			retry={() => {
				templates.reload();
				usage.reload();
			}}
		/></Card
	>
{:else if !templates.data || !usage.data}
	<Card><Skeleton lines={6} /></Card>
{:else}
	<ol class="mb-6 flex flex-wrap items-center gap-2 text-sm" aria-label="Progress">
		{#each steps as label, i (label)}
			{@const n = i + 1}
			<li class="flex items-center gap-2" aria-current={step === n ? 'step' : undefined}>
				<span
					class="flex size-6 items-center justify-center rounded-full text-xs font-semibold {step >
					n
						? 'bg-success-soft text-success'
						: step === n
							? 'bg-accent text-accent-fg'
							: 'bg-panel-2 text-fg-muted'}"
				>
					{#if step > n}<Check class="size-3.5" aria-label="Done" />{:else}{n}{/if}
				</span>
				<span class={step === n ? 'font-medium text-fg' : 'text-fg-muted'}>{label}</span>
				{#if n < steps.length}<span class="mx-1 h-px w-8 bg-border" aria-hidden="true"></span>{/if}
			</li>
		{/each}
	</ol>

	{#if quota && used}
		<div class="mb-4 flex flex-wrap gap-x-6 gap-y-1 text-xs text-fg-muted">
			<span>Quota package: <strong class="text-fg">{quota.name}</strong></span>
			<span
				>Workspaces: <strong class="text-fg">{used.workspaces} of {quota.max_workspaces}</strong> used</span
			>
			<span
				>Storage left: <strong class="text-fg">{bytes(remainingStorage)}</strong> of {bytes(
					quota.storage_bytes
				)}</span
			>
		</div>
		{#if remainingWorkspaces === 0}
			<div class="mb-4">
				<Alert tone="warning" title="Workspace limit reached">
					You already have {used.workspaces} of {quota.max_workspaces} workspaces allowed by your quota.
					Delete an unused workspace or ask an administrator for a larger quota.
				</Alert>
			</div>
		{/if}
	{/if}

	{#if formError}
		<div class="mb-4">
			<Alert tone="danger" title={formError.title}>{formError.message}</Alert>
		</div>
	{/if}

	{#if step === 1}
		{#if templates.data.length === 0}
			<Card>
				<EmptyState
					title="No templates available"
					description="An administrator has to publish at least one template before workspaces can be created."
				/>
			</Card>
		{:else}
			<fieldset>
				<legend class="mb-3 text-sm font-medium text-fg">Choose a template</legend>
				{#if errors.template_id}
					<p class="mb-3 text-sm text-danger" role="alert">{errors.template_id}</p>
				{/if}
				<div class="space-y-6">
					{#each grouped as [category, list] (category)}
						<div>
							<h2 class="mb-2 text-xs font-semibold tracking-wide text-fg-muted uppercase">
								{titleCase(category || 'other')}
							</h2>
							<div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
								{#each list as t (t.id)}
									{@const reason = unusable(t)}
									<label
										class="block rounded-lg has-[input:focus-visible]:ring-2 has-[input:focus-visible]:ring-accent {reason
											? 'cursor-not-allowed opacity-60'
											: 'cursor-pointer'}"
									>
										<input
											type="radio"
											name="template"
											class="sr-only"
											value={t.id}
											checked={templateId === t.id}
											disabled={!!reason}
											onchange={() => selectTemplate(t)}
										/>
										<TemplateCard template={t} selected={templateId === t.id} note={reason} />
									</label>
								{/each}
							</div>
						</div>
					{/each}
				</div>
			</fieldset>
		{/if}
	{:else if step === 2 && tpl}
		<Card
			title="Configure {tpl.name}"
			description="Limits are bounded by the template and your quota."
		>
			<form
				class="grid gap-5 md:grid-cols-2"
				novalidate
				onsubmit={(e) => {
					e.preventDefault();
					next();
				}}
			>
				<div class="md:col-span-2">
					<Field label="Name" id="ws-name" error={errors.name} required hint="Up to 80 characters.">
						<input
							id="ws-name"
							class="input"
							bind:value={name}
							maxlength="80"
							autocomplete="off"
							aria-invalid={!!errors.name}
						/>
					</Field>
				</div>

				<div class="md:col-span-2">
					<Field label="Type" id="ws-type" error={errors.type}>
						<select id="ws-type" class="input" bind:value={type} disabled={!nativeAllowed}>
							<option value="container">Container (isolated, recommended)</option>
							{#if nativeAllowed}<option value="native">Linux-native (host user account)</option
								>{/if}
						</select>
					</Field>
				</div>

				<Field
					label="CPU (cores)"
					id="ws-cpu"
					error={errors.cpu_millis}
					hint="Between {bounds.cpuMin} and {bounds.cpuMax} cores. Recommended: {cores(
						tpl.rec_cpu_millis
					)}."
				>
					<div class="flex items-center gap-3">
						<input
							type="range"
							class="flex-1 accent-accent"
							min={bounds.cpuMin}
							max={bounds.cpuMax}
							step="0.25"
							bind:value={cpu}
							aria-label="CPU cores slider"
						/>
						<input
							id="ws-cpu"
							type="number"
							class="input w-24"
							min={bounds.cpuMin}
							max={bounds.cpuMax}
							step="0.25"
							bind:value={cpu}
							aria-invalid={!!errors.cpu_millis}
						/>
					</div>
				</Field>

				<Field
					label="Memory (GiB)"
					id="ws-mem"
					error={errors.memory_bytes}
					hint="Between {bounds.memMin} and {bounds.memMax} GiB. Recommended: {bytes(
						tpl.rec_memory_bytes
					)}."
				>
					<div class="flex items-center gap-3">
						<input
							type="range"
							class="flex-1 accent-accent"
							min={bounds.memMin}
							max={bounds.memMax}
							step="0.25"
							bind:value={memory}
							aria-label="Memory slider"
						/>
						<input
							id="ws-mem"
							type="number"
							class="input w-24"
							min={bounds.memMin}
							max={bounds.memMax}
							step="0.25"
							bind:value={memory}
							aria-invalid={!!errors.memory_bytes}
						/>
					</div>
				</Field>

				<Field
					label="Storage (GiB)"
					id="ws-storage"
					error={errors.storage_bytes}
					hint="Persistent volume. {bytes(remainingStorage)} of your storage quota is left."
				>
					<div class="flex items-center gap-3">
						<input
							type="range"
							class="flex-1 accent-accent"
							min={bounds.storageMin}
							max={Math.max(bounds.storageMin, bounds.storageMax)}
							step="0.25"
							bind:value={storage}
							disabled={bounds.storageMax < bounds.storageMin}
							aria-label="Storage slider"
						/>
						<input
							id="ws-storage"
							type="number"
							class="input w-24"
							min={bounds.storageMin}
							max={bounds.storageMax}
							step="0.25"
							bind:value={storage}
							aria-invalid={!!errors.storage_bytes}
						/>
					</div>
				</Field>

				<Field
					label="Idle timeout (minutes)"
					id="ws-idle"
					error={errors.idle_timeout_min}
					hint="Stops the workspace after this long without activity. Leave empty for your quota default ({idleLabel(
						quota?.idle_timeout_min
					)})."
				>
					<input
						id="ws-idle"
						type="number"
						class="input"
						min="5"
						max="10080"
						step="1"
						placeholder="Quota default"
						bind:value={idle}
						aria-invalid={!!errors.idle_timeout_min}
					/>
				</Field>
				<button type="submit" class="hidden" tabindex="-1">Next</button>
			</form>
		</Card>
	{:else if step === 3 && tpl}
		<div class="grid gap-6 lg:grid-cols-2">
			<Card title="Project" description="Optionally share this workspace with a project.">
				{#if projects.error}
					<Alert tone="warning" title="Projects could not be loaded">
						{errorMessage(projects.error)} You can still create a personal workspace.
					</Alert>
				{:else if !projects.data}
					<Skeleton lines={2} />
				{:else}
					<Field
						label="Project"
						id="ws-project"
						error={errors.project_id}
						hint={projectChoices.length
							? 'Project owners and maintainers can operate workspaces in the project.'
							: 'You are not a member (above viewer) of any active project.'}
					>
						<select id="ws-project" class="input" bind:value={projectId}>
							<option value="">No project (personal)</option>
							{#each projectChoices as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
						</select>
					</Field>
				{/if}
			</Card>

			<Card title="Review">
				<dl class="grid grid-cols-[auto_1fr] gap-x-6 gap-y-2 text-sm">
					<dt class="text-fg-muted">Name</dt>
					<dd class="font-medium">{name}</dd>
					<dt class="text-fg-muted">Template</dt>
					<dd>{tpl.name}</dd>
					<dt class="text-fg-muted">Type</dt>
					<dd>{type === 'native' ? 'Linux-native' : 'Container'}</dd>
					<dt class="text-fg-muted">CPU</dt>
					<dd>{cores(toUnits(cpu, 1000))}</dd>
					<dt class="text-fg-muted">Memory</dt>
					<dd>{bytes(toUnits(memory, GiB))}</dd>
					<dt class="text-fg-muted">Storage</dt>
					<dd>{bytes(toUnits(storage, GiB))}</dd>
					<dt class="text-fg-muted">Idle timeout</dt>
					<dd>
						{idle === null
							? `Quota default (${idleLabel(quota?.idle_timeout_min)})`
							: idleLabel(idle)}
					</dd>
					<dt class="text-fg-muted">Project</dt>
					<dd>{selectedProject?.name ?? 'None (personal)'}</dd>
					{#if tpl.gpu_mode !== 'none'}
						<dt class="text-fg-muted">GPU</dt>
						<dd>
							{tpl.gpu_mode === 'required' ? 'Required' : 'Optional'} — request a GPU from the GPU Center
							after creation.
						</dd>
					{/if}
				</dl>
				{#if !tpl.image_present}
					<p class="mt-4 text-xs text-warning">
						The template image is not installed on this server; only a Linux-native workspace can
						use this template.
					</p>
				{/if}
			</Card>
		</div>
	{/if}

	<div class="mt-6 flex items-center justify-between gap-3">
		<Button
			variant="ghost"
			disabled={step === 1 || submitting}
			onclick={() => {
				formError = null;
				step -= 1;
			}}><ArrowLeft class="size-4" /> Previous</Button
		>
		{#if step < 3}
			<Button variant="primary" onclick={next} disabled={step === 1 && !tpl}>
				Next <ArrowRight class="size-4" />
			</Button>
		{:else}
			<Button
				variant="primary"
				loading={submitting}
				disabled={remainingWorkspaces === 0}
				onclick={submit}><Rocket class="size-4" /> Create workspace</Button
			>
		{/if}
	</div>
{/if}
