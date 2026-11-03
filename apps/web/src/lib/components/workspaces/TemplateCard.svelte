<script lang="ts">
	// Template summary used by the catalog and the create-workspace wizard.
	import type { Snippet } from 'svelte';
	import { Cpu, ImageOff, MemoryStick, Network } from '@lucide/svelte';
	import type { Template } from '#lib/api/types.ts';
	import { Badge } from '#lib/components/ui/index.ts';
	import { bytes, cores } from '#lib/format.ts';

	let {
		template: t,
		selected = false,
		note,
		actions
	}: {
		template: Template;
		selected?: boolean;
		/** Warning shown at the bottom (e.g. why the template cannot be used). */
		note?: string;
		actions?: Snippet;
	} = $props();

	const versions = $derived(Object.entries(t.runtime_versions ?? {}));
</script>

<div
	class="card flex h-full flex-col p-4 transition-colors {selected
		? 'border-accent ring-2 ring-accent/40'
		: ''}"
>
	<div class="flex items-start justify-between gap-2">
		<div class="min-w-0">
			<h3 class="truncate text-sm font-semibold text-fg">{t.name}</h3>
			<p class="truncate font-mono text-xs text-fg-subtle" title={t.image_reference}>
				{t.image_reference}
			</p>
		</div>
		<div class="flex shrink-0 flex-wrap justify-end gap-1">
			{#if t.gpu_mode === 'required'}<Badge tone="warning">GPU required</Badge>
			{:else if t.gpu_mode === 'optional'}<Badge tone="info">GPU optional</Badge>{/if}
		</div>
	</div>
	{#if t.description}<p class="mt-2 line-clamp-3 text-sm text-fg-muted">{t.description}</p>{/if}

	{#if versions.length}
		<div class="mt-3 flex flex-wrap gap-1">
			{#each versions as [name, version] (name)}
				<Badge tone="neutral">{name} {version}</Badge>
			{/each}
		</div>
	{/if}

	<dl class="mt-3 grid grid-cols-2 gap-x-3 gap-y-1 text-xs">
		<dt class="flex items-center gap-1 text-fg-muted">
			<Cpu class="size-3.5" aria-hidden="true" /> CPU
		</dt>
		<dd class="text-fg tabular-nums">
			min {cores(t.min_cpu_millis)} · rec. {cores(t.rec_cpu_millis)}
		</dd>
		<dt class="flex items-center gap-1 text-fg-muted">
			<MemoryStick class="size-3.5" aria-hidden="true" /> Memory
		</dt>
		<dd class="text-fg tabular-nums">
			min {bytes(t.min_memory_bytes)} · rec. {bytes(t.rec_memory_bytes)}
		</dd>
		<dt class="flex items-center gap-1 text-fg-muted">
			<Network class="size-3.5" aria-hidden="true" /> Ports
		</dt>
		<dd class="text-fg">
			{#if t.ports?.length}
				{t.ports.map((p) => `${p.port}${p.name ? ` (${p.name})` : ''}`).join(', ')}
			{:else}<span class="text-fg-muted">None</span>{/if}
		</dd>
	</dl>

	{#if !t.image_present}
		<p class="mt-3 flex items-center gap-1.5 text-xs text-warning">
			<ImageOff class="size-3.5 shrink-0" aria-hidden="true" />
			Image not installed on this server yet.
		</p>
	{/if}
	{#if note}<p class="mt-2 text-xs text-danger">{note}</p>{/if}

	{#if actions}<div class="mt-auto flex justify-end gap-2 pt-4">{@render actions()}</div>{/if}
</div>
