<script lang="ts">
	import { page } from '$app/state';
	import { ChevronRight } from '@lucide/svelte';
	import { segmentLabels } from './nav';
	import { crumbs } from '#lib/stores/breadcrumb.svelte.ts';
	import { titleCase } from '#lib/format.ts';

	const items = $derived.by(() => {
		const segs = page.url.pathname
			.split('/')
			.filter(Boolean)
			.filter((s, i) => !(i === 0 && s === 'dashboard'));
		const out = [{ href: '/dashboard', label: 'Home' }];
		let acc = '';
		for (const s of segs) {
			acc += '/' + s;
			const label =
				crumbs.labels[s] ??
				segmentLabels[s] ??
				(/^[0-9a-f-]{36}$/.test(s) ? s.slice(0, 8) : titleCase(s));
			out.push({ href: acc, label });
		}
		return out;
	});
</script>

<nav aria-label="Breadcrumb">
	<ol class="flex min-w-0 items-center gap-1 text-sm text-fg-muted">
		{#each items as c, i (c.href)}
			<li class="flex min-w-0 items-center gap-1">
				{#if i > 0}<ChevronRight class="size-3.5 shrink-0 text-fg-subtle" aria-hidden="true" />{/if}
				{#if i === items.length - 1}
					<span class="truncate font-medium text-fg" aria-current="page">{c.label}</span>
				{:else}
					<a href={c.href} class="truncate hover:text-fg">{c.label}</a>
				{/if}
			</li>
		{/each}
	</ol>
</nav>
