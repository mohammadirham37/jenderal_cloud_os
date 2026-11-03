<script lang="ts">
	import { RotateCcw, Save } from '@lucide/svelte';
	import {
		Badge,
		Button,
		Card,
		ErrorState,
		PageHeader,
		Skeleton,
		Switch
	} from '#lib/components/ui/index.ts';
	import { api } from '#lib/api/client.ts';
	import { resource } from '#lib/api/resource.svelte.ts';
	import type { Quota, Setting } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';
	import { titleCase } from '#lib/format.ts';
	import AccessDenied from '#lib/components/admin/AccessDenied.svelte';
	import { guarded } from '#lib/components/admin/access.ts';
	import { handleFormError } from '#lib/components/admin/forms.ts';
	import { groupBy } from '#lib/components/admin/group.ts';

	const allowed = $derived(auth.can('settings.manage'));
	const settings = resource(
		guarded(['settings.manage'], () => api.get<Setting[]>('/admin/settings'))
	);
	const quotas = resource(
		guarded(['quotas.view', 'quotas.manage'], () => api.get<Quota[]>('/quotas'))
	);

	let draft = $state<Record<string, unknown>>({});
	let errors = $state<Record<string, string>>({});
	let saving = $state(false);
	let syncedWith: Setting[] | undefined;

	// Re-initialise the draft whenever fresh data arrives from the server.
	$effect(() => {
		const data = settings.data;
		if (data && data !== syncedWith) {
			syncedWith = data;
			draft = Object.fromEntries(data.map((s) => [s.key, s.value]));
		}
	});

	const categoryOrder = [
		'platform',
		'security',
		'quota',
		'workspaces',
		'scheduler',
		'gpu',
		'backup',
		'monitoring',
		'alerts',
		'audit'
	];
	const groups = $derived.by(() => {
		const rank = (c: string) => {
			const i = categoryOrder.indexOf(c);
			return i < 0 ? 99 : i;
		};
		return groupBy(settings.data ?? [], (s) => s.category).sort((a, b) => rank(a[0]) - rank(b[0]));
	});

	const changed = $derived(
		(settings.data ?? []).filter((s) => s.key in draft && draft[s.key] !== s.value)
	);
	const isDefault = (s: Setting) => draft[s.key] === s.default;

	function setInt(key: string, raw: string) {
		draft[key] = raw === '' ? '' : Number(raw);
	}

	async function save() {
		const values: Record<string, unknown> = {};
		const local: Record<string, string> = {};
		for (const s of changed) {
			const v = draft[s.key];
			if (s.type === 'int') {
				if (typeof v !== 'number' || !Number.isInteger(v)) {
					local[s.key] = 'Enter a whole number.';
					continue;
				}
				if (s.min !== undefined && v < s.min) local[s.key] = `Must be at least ${s.min}.`;
				else if (s.max !== undefined && v > s.max) local[s.key] = `Must be at most ${s.max}.`;
			}
			values[s.key] = v;
		}
		errors = local;
		if (Object.keys(local).length) {
			toast.error('Some values are invalid', 'Check the highlighted settings.');
			return;
		}
		saving = true;
		try {
			const updated = await api.patch<Setting[]>('/admin/settings', { values });
			settings.set(updated);
			errors = {};
			toast.success('Settings saved', `${Object.keys(values).length} setting(s) changed.`);
		} catch (err) {
			errors = handleFormError(err, 'Could not save the settings');
		} finally {
			saving = false;
		}
	}

	function discard() {
		if (settings.data) draft = Object.fromEntries(settings.data.map((s) => [s.key, s.value]));
		errors = {};
	}
	const fid = (key: string) => `s-${key.replace(/\./g, '-')}`;
	const display = (v: unknown) => (typeof v === 'boolean' ? (v ? 'on' : 'off') : String(v));
</script>

<PageHeader
	title="Platform Settings"
	description="Runtime settings stored in the database. Changes take effect within a minute; every change is audited."
>
	{#snippet actions()}
		{#if allowed && settings.data}
			<Button onclick={discard} disabled={changed.length === 0 || saving}>Discard</Button>
			<Button variant="primary" onclick={save} loading={saving} disabled={changed.length === 0}
				><Save class="size-4" /> Save changes{changed.length ? ` (${changed.length})` : ''}</Button
			>
		{/if}
	{/snippet}
</PageHeader>

{#if !allowed}
	<AccessDenied />
{:else if settings.error && !settings.data}
	<Card><ErrorState error={settings.error} retry={settings.reload} /></Card>
{:else if !settings.data}
	<div class="space-y-6">
		{#each [1, 2, 3] as i (i)}<div class="card p-5"><Skeleton lines={4} /></div>{/each}
	</div>
{:else}
	<form
		class="space-y-6"
		onsubmit={(e) => {
			e.preventDefault();
			save();
		}}
	>
		{#each groups as [category, list] (category)}
			<Card title={titleCase(category)} padded={false}>
				<ul class="divide-y divide-border">
					{#each list as s (s.key)}
						{@const id = fid(s.key)}
						{@const dirty = draft[s.key] !== s.value}
						<li
							class="grid gap-3 px-5 py-4 md:grid-cols-[1fr_minmax(0,18rem)_auto] md:items-center"
						>
							<div class="min-w-0">
								<label for={id} class="text-sm font-medium text-fg">{s.description}</label>
								<p class="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-fg-muted">
									<span class="font-mono">{s.key}</span>
									<span>default: {display(s.default)}</span>
									{#if s.type === 'int' && (s.min !== undefined || s.max !== undefined)}
										<span>range {s.min ?? '…'}–{s.max ?? '…'}</span>
									{/if}
									{#if dirty}<Badge tone="warning">Unsaved</Badge>{/if}
								</p>
								{#if errors[s.key]}
									<p id="{id}-error" class="mt-1 text-xs text-danger" role="alert">
										{errors[s.key]}
									</p>
								{/if}
							</div>
							<div>
								{#if s.type === 'bool'}
									<Switch
										{id}
										label={s.description}
										checked={draft[s.key] === true}
										onchange={(v) => (draft[s.key] = v)}
									/>
								{:else if s.type === 'int'}
									<input
										{id}
										type="number"
										class="input"
										step="1"
										min={s.min}
										max={s.max}
										value={draft[s.key] as number}
										oninput={(e) => setInt(s.key, e.currentTarget.value)}
										aria-invalid={!!errors[s.key]}
										aria-describedby={errors[s.key] ? `${id}-error` : undefined}
									/>
								{:else if s.type === 'enum' && s.options?.length}
									<select
										{id}
										class="input"
										value={draft[s.key] as string}
										onchange={(e) => (draft[s.key] = e.currentTarget.value)}
									>
										{#each s.options as o (o)}<option value={o}>{o}</option>{/each}
									</select>
								{:else}
									<input
										{id}
										class="input"
										maxlength={s.max || undefined}
										value={(draft[s.key] as string) ?? ''}
										oninput={(e) => (draft[s.key] = e.currentTarget.value)}
										list={s.key === 'quota.default_package' && quotas.data
											? 'quota-keys'
											: undefined}
										aria-invalid={!!errors[s.key]}
										aria-describedby={errors[s.key] ? `${id}-error` : undefined}
									/>
								{/if}
							</div>
							<div class="md:text-right">
								<Button
									size="sm"
									variant="ghost"
									disabled={isDefault(s)}
									onclick={() => {
										// Rebuild the record so key-wise reactivity also updates controlled inputs.
										draft = { ...draft, [s.key]: s.default };
									}}
									title="Reset to default ({display(s.default)})"
									aria-label="Reset {s.key} to default"
									><RotateCcw class="size-3.5" /> Default</Button
								>
							</div>
						</li>
					{/each}
				</ul>
			</Card>
		{/each}
		<datalist id="quota-keys">
			{#each quotas.data ?? [] as q (q.id)}<option value={q.key}>{q.name}</option>{/each}
		</datalist>
	</form>
{/if}
