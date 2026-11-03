<script lang="ts" generics="T">
	// Table shell with loading, empty and error states. Rows are rendered by
	// the caller through the `row` snippet so each page controls its columns.
	import type { Snippet } from 'svelte';
	import Skeleton from './Skeleton.svelte';
	import ErrorState from './ErrorState.svelte';
	import EmptyState from './EmptyState.svelte';

	interface Column {
		key: string;
		label: string;
		sortable?: boolean;
		class?: string;
	}
	let {
		columns,
		rows,
		loading = false,
		error = null,
		retry,
		sort = $bindable(''),
		onsort,
		row,
		empty,
		emptyTitle = 'Nothing here yet',
		emptyDescription
	}: {
		columns: Column[];
		rows: T[];
		loading?: boolean;
		error?: unknown;
		retry?: () => void;
		sort?: string;
		onsort?: (sort: string) => void;
		row: Snippet<[T]>;
		empty?: Snippet;
		emptyTitle?: string;
		emptyDescription?: string;
	} = $props();

	function toggle(key: string) {
		sort = sort === key ? `-${key}` : key;
		onsort?.(sort);
	}
	function dir(key: string): 'ascending' | 'descending' | 'none' {
		if (sort === key) return 'ascending';
		if (sort === `-${key}`) return 'descending';
		return 'none';
	}
</script>

{#if error}
	<ErrorState {error} {retry} />
{:else if loading && rows.length === 0}
	<div class="p-5"><Skeleton lines={5} /></div>
{:else if rows.length === 0}
	{#if empty}{@render empty()}{:else}<EmptyState
			title={emptyTitle}
			description={emptyDescription}
		/>{/if}
{:else}
	<div class="overflow-x-auto">
		<table class="w-full text-left text-sm" aria-busy={loading}>
			<thead
				class="border-b border-border bg-panel-2/60 text-xs tracking-wide text-fg-muted uppercase"
			>
				<tr>
					{#each columns as c (c.key)}
						<th
							scope="col"
							class="px-4 py-2.5 font-medium {c.class ?? ''}"
							aria-sort={c.sortable ? dir(c.key) : undefined}
						>
							{#if c.sortable}
								<button
									type="button"
									class="inline-flex items-center gap-1 uppercase hover:text-fg"
									onclick={() => toggle(c.key)}
								>
									{c.label}<span aria-hidden="true"
										>{dir(c.key) === 'ascending'
											? '↑'
											: dir(c.key) === 'descending'
												? '↓'
												: ''}</span
									>
								</button>
							{:else}{c.label}{/if}
						</th>
					{/each}
				</tr>
			</thead>
			<tbody class="divide-y divide-border">
				{#each rows as r, i (i)}
					{@render row(r)}
				{/each}
			</tbody>
		</table>
	</div>
{/if}
