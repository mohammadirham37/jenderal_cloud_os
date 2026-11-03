<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { HTMLButtonAttributes } from 'svelte/elements';
	import Spinner from './Spinner.svelte';

	interface Props extends HTMLButtonAttributes {
		variant?: 'primary' | 'secondary' | 'danger' | 'ghost' | 'subtle';
		size?: 'sm' | 'md';
		loading?: boolean;
		href?: string;
		/** Link-only attributes (used when `href` is set). */
		download?: string | boolean;
		target?: string;
		rel?: string;
		children?: Snippet;
	}

	let {
		variant = 'secondary',
		size = 'md',
		loading = false,
		href,
		download,
		target,
		rel,
		disabled,
		type = 'button',
		class: klass = '',
		children,
		...rest
	}: Props = $props();

	const variants = {
		primary: 'bg-accent text-accent-fg hover:bg-accent-hover border-transparent shadow-sm',
		secondary: 'bg-panel text-fg border-border-strong hover:bg-panel-2 shadow-sm',
		danger: 'bg-danger text-white hover:opacity-90 border-transparent shadow-sm',
		ghost: 'bg-transparent text-fg-muted hover:bg-panel-2 hover:text-fg border-transparent',
		subtle: 'bg-accent-soft text-accent hover:opacity-90 border-transparent'
	};
	const sizes = { sm: 'h-8 px-2.5 text-xs gap-1.5', md: 'h-9 px-3.5 text-sm gap-2' };
	const cls = $derived(
		`inline-flex items-center justify-center rounded-md border font-medium whitespace-nowrap transition-colors disabled:cursor-not-allowed disabled:opacity-60 ${variants[variant]} ${sizes[size]} ${klass}`
	);
</script>

{#if href && !disabled}
	<a
		{href}
		class={cls}
		download={download === true ? '' : download || undefined}
		{target}
		rel={rel ?? (target === '_blank' ? 'noopener noreferrer' : undefined)}
		title={rest.title ?? undefined}
		aria-label={rest['aria-label'] ?? undefined}>{@render children?.()}</a
	>
{:else}
	<button {type} class={cls} disabled={disabled || loading} aria-busy={loading} {...rest}>
		{#if loading}<Spinner size={14} />{/if}
		{@render children?.()}
	</button>
{/if}
