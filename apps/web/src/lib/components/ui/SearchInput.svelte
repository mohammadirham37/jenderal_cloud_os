<script lang="ts">
	import { Search } from '@lucide/svelte';

	let {
		value = $bindable(''),
		placeholder = 'Search…',
		onsearch,
		delay = 300
	}: {
		value?: string;
		placeholder?: string;
		onsearch?: (v: string) => void;
		delay?: number;
	} = $props();
	let timer: ReturnType<typeof setTimeout>;

	function oninput() {
		clearTimeout(timer);
		timer = setTimeout(() => onsearch?.(value), delay);
	}
</script>

<div class="relative">
	<Search
		class="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-fg-subtle"
		aria-hidden="true"
	/>
	<input
		type="search"
		class="input pl-8"
		{placeholder}
		bind:value
		{oninput}
		aria-label={placeholder}
	/>
</div>
