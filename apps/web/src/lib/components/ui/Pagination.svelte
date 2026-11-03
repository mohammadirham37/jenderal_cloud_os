<script lang="ts">
	import { ChevronLeft, ChevronRight } from '@lucide/svelte';

	let {
		page = $bindable(1),
		perPage,
		total,
		onchange
	}: { page: number; perPage: number; total: number; onchange?: (p: number) => void } = $props();
	const pages = $derived(Math.max(1, Math.ceil(total / perPage)));
	const from = $derived(total === 0 ? 0 : (page - 1) * perPage + 1);
	const to = $derived(Math.min(total, page * perPage));

	function go(p: number) {
		page = Math.min(pages, Math.max(1, p));
		onchange?.(page);
	}
</script>

<nav
	class="flex items-center justify-between gap-3 px-1 py-3 text-sm text-fg-muted"
	aria-label="Pagination"
>
	<span>{from}–{to} of {total}</span>
	<div class="flex items-center gap-1">
		<button
			class="rounded-md border border-border p-1.5 hover:bg-panel-2 disabled:opacity-40"
			disabled={page <= 1}
			onclick={() => go(page - 1)}
			aria-label="Previous page"
		>
			<ChevronLeft class="size-4" />
		</button>
		<span class="px-2 tabular-nums">Page {page} / {pages}</span>
		<button
			class="rounded-md border border-border p-1.5 hover:bg-panel-2 disabled:opacity-40"
			disabled={page >= pages}
			onclick={() => go(page + 1)}
			aria-label="Next page"
		>
			<ChevronRight class="size-4" />
		</button>
	</div>
</nav>
