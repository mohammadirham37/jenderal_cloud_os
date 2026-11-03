<script lang="ts">
	import type { FileEntry } from '#lib/api/types.ts';
	import TreeNode from './TreeNode.svelte';

	let {
		selected,
		version,
		load,
		onselect
	}: {
		selected: string;
		/** Increment to reload expanded folders. */
		version: number;
		load: (path: string) => Promise<FileEntry[]>;
		onselect: (path: string) => void;
	} = $props();
</script>

<nav aria-label="Folder tree">
	<ul role="tree" class="space-y-0.5">
		<TreeNode path="" name="Home" {selected} {version} {load} {onselect} />
	</ul>
</nav>
