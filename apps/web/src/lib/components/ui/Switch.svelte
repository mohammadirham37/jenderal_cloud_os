<script lang="ts">
	let {
		checked = $bindable(false),
		label,
		id,
		disabled = false,
		onchange
	}: {
		checked?: boolean;
		label: string;
		id: string;
		disabled?: boolean;
		onchange?: (v: boolean) => void;
	} = $props();
</script>

<button
	{id}
	type="button"
	role="switch"
	aria-checked={checked}
	aria-label={label}
	{disabled}
	class="relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition-colors disabled:opacity-50 {checked
		? 'bg-accent'
		: 'bg-border-strong'}"
	onclick={() => {
		checked = !checked;
		onchange?.(checked);
	}}
>
	<span
		class="inline-block size-4 rounded-full bg-white shadow transition-transform {checked
			? 'translate-x-4.5'
			: 'translate-x-0.5'}"
	></span>
</button>
