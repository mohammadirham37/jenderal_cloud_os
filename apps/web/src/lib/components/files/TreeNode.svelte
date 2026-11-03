<script lang="ts">
	// One folder in the lazily loaded folder tree. Children are fetched when
	// the node is expanded; nodes on the path to the selected folder expand
	// automatically.
	import { untrack } from 'svelte';
	import { ChevronRight, Folder, FolderOpen, House } from '@lucide/svelte';
	import type { FileEntry } from '#lib/api/types.ts';
	import { sortEntries, within } from './paths.ts';
	import TreeNode from './TreeNode.svelte';

	let {
		path,
		name,
		depth = 0,
		selected,
		version,
		load,
		onselect
	}: {
		path: string;
		name: string;
		depth?: number;
		selected: string;
		version: number;
		load: (path: string) => Promise<FileEntry[]>;
		onselect: (path: string) => void;
	} = $props();

	let children = $state<FileEntry[] | null>(null);
	let expanded = $state(false);
	let loading = $state(false);
	let failed = $state(false);
	let seenVersion = untrack(() => version);

	async function loadChildren() {
		loading = true;
		try {
			children = sortEntries((await load(path)).filter((e) => e.type === 'dir'));
			failed = false;
		} catch {
			failed = true;
		} finally {
			loading = false;
		}
	}

	function toggle() {
		expanded = !expanded;
		if (expanded && children === null) loadChildren();
	}

	// Expand the ancestors of the selected folder.
	$effect(() => {
		const s = selected;
		if (path === '' || within(s, path)) {
			untrack(() => {
				if (!expanded) {
					expanded = true;
					if (children === null && !loading) loadChildren();
				}
			});
		}
	});

	// Reload expanded nodes when the page refreshes.
	$effect(() => {
		const v = version;
		untrack(() => {
			if (v !== seenVersion) {
				seenVersion = v;
				if (expanded) loadChildren();
				else children = null;
			}
		});
	});

	const isSelected = $derived(selected === path);
	const Icon = $derived(path === '' ? House : expanded ? FolderOpen : Folder);
</script>

<li role="treeitem" aria-expanded={expanded} aria-selected={isSelected}>
	<div
		class="flex items-center gap-1 rounded-md pr-2 text-sm {isSelected
			? 'bg-accent-soft text-accent'
			: 'text-fg hover:bg-panel-2'}"
		style="padding-left: {depth * 0.75 + 0.25}rem"
	>
		<button
			type="button"
			class="rounded p-0.5 text-fg-subtle hover:text-fg"
			onclick={toggle}
			aria-label="{expanded ? 'Collapse' : 'Expand'} {name}"
			tabindex="-1"
		>
			<ChevronRight
				class="size-3.5 transition-transform {expanded ? 'rotate-90' : ''}"
				aria-hidden="true"
			/>
		</button>
		<button
			type="button"
			class="flex min-w-0 flex-1 items-center gap-1.5 py-1 text-left"
			onclick={() => onselect(path)}
			onkeydown={(e) => {
				if (e.key === 'ArrowRight' && !expanded) {
					e.preventDefault();
					toggle();
				} else if (e.key === 'ArrowLeft' && expanded) {
					e.preventDefault();
					toggle();
				}
			}}
			title={path || 'Home'}
		>
			<Icon class="size-4 shrink-0 {isSelected ? '' : 'text-accent'}" aria-hidden="true" />
			<span class="truncate">{name}</span>
		</button>
	</div>
	{#if expanded}
		{#if loading && children === null}
			<p class="py-1 text-xs text-fg-subtle" style="padding-left: {(depth + 1) * 0.75 + 1.5}rem">
				Loading…
			</p>
		{:else if failed && children === null}
			<p class="py-1 text-xs text-danger" style="padding-left: {(depth + 1) * 0.75 + 1.5}rem">
				Could not load folder.
				<button type="button" class="link" onclick={loadChildren}>Retry</button>
			</p>
		{:else if children && children.length > 0}
			<ul role="group">
				{#each children as c (c.path)}
					<TreeNode
						path={c.path}
						name={c.name}
						depth={depth + 1}
						{selected}
						{version}
						{load}
						{onselect}
					/>
				{/each}
			</ul>
		{/if}
	{/if}
</li>
