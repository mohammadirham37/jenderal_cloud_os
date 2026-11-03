<script lang="ts">
	// Editable list of key/value pairs (runtime versions, environment
	// variables). Empty keys are ignored by `toRecord`.
	import { Plus, Trash2 } from '@lucide/svelte';
	import { Button } from '#lib/components/ui/index.ts';
	import type { KV } from './kv.ts';

	let {
		rows = $bindable([]),
		id,
		keyPlaceholder = 'KEY',
		valuePlaceholder = 'value',
		addLabel = 'Add'
	}: {
		rows: KV[];
		id: string;
		keyPlaceholder?: string;
		valuePlaceholder?: string;
		addLabel?: string;
	} = $props();
</script>

<div class="space-y-2" {id}>
	{#each rows as row, i (i)}
		<div class="flex items-center gap-2">
			<input
				class="input font-mono"
				placeholder={keyPlaceholder}
				aria-label="Key {i + 1}"
				bind:value={row.key}
			/>
			<input
				class="input font-mono"
				placeholder={valuePlaceholder}
				aria-label="Value {i + 1}"
				bind:value={row.value}
			/>
			<Button
				variant="ghost"
				size="sm"
				aria-label="Remove row {i + 1}"
				onclick={() => (rows = rows.filter((_, j) => j !== i))}><Trash2 class="size-4" /></Button
			>
		</div>
	{/each}
	<Button size="sm" onclick={() => (rows = [...rows, { key: '', value: '' }])}
		><Plus class="size-3.5" /> {addLabel}</Button
	>
</div>
