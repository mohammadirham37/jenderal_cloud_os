<script lang="ts">
	let { size = 16, class: klass = '' }: { size?: number; class?: string } = $props();
</script>

<svg
	class="animate-spin {klass}"
	width={size}
	height={size}
	viewBox="0 0 24 24"
	fill="none"
	role="status"
	aria-label="Loading"
>
	<circle cx="12" cy="12" r="10" stroke="currentColor" stroke-width="3" opacity="0.25" />
	<path
		d="M22 12a10 10 0 0 0-10-10"
		stroke="currentColor"
		stroke-width="3"
		stroke-linecap="round"
	/>
</svg>
