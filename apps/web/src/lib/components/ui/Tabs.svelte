<script lang="ts">
	interface Tab {
		id: string;
		label: string;
		count?: number;
	}
	let {
		tabs,
		active = $bindable(),
		onchange
	}: { tabs: Tab[]; active: string; onchange?: (id: string) => void } = $props();

	function select(id: string) {
		active = id;
		onchange?.(id);
	}

	function onkeydown(e: KeyboardEvent, i: number) {
		if (e.key !== 'ArrowRight' && e.key !== 'ArrowLeft') return;
		e.preventDefault();
		const next = (i + (e.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
		select(tabs[next].id);
		document.getElementById(`tab-${tabs[next].id}`)?.focus();
	}
</script>

<div class="flex gap-1 overflow-x-auto border-b border-border" role="tablist">
	{#each tabs as tab, i (tab.id)}
		<button
			id="tab-{tab.id}"
			role="tab"
			type="button"
			aria-selected={active === tab.id}
			tabindex={active === tab.id ? 0 : -1}
			class="-mb-px border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap transition-colors {active ===
			tab.id
				? 'border-accent text-fg'
				: 'border-transparent text-fg-muted hover:text-fg'}"
			onclick={() => select(tab.id)}
			onkeydown={(e) => onkeydown(e, i)}
		>
			{tab.label}{#if tab.count !== undefined}<span
					class="ml-1.5 rounded-full bg-panel-2 px-1.5 text-xs">{tab.count}</span
				>{/if}
		</button>
	{/each}
</div>
