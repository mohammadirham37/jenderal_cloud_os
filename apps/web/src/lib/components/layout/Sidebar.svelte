<script lang="ts">
	import { page } from '$app/state';
	import { PanelLeftClose, PanelLeftOpen, Cloud } from '@lucide/svelte';
	import { navGroups } from './nav';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { appConfig } from '#lib/stores/config.svelte.ts';

	let {
		collapsed = $bindable(false),
		mobileOpen = $bindable(false)
	}: { collapsed?: boolean; mobileOpen?: boolean } = $props();

	const groups = $derived(
		navGroups
			.map((g) => ({
				...g,
				items: g.items.filter((i) => !i.perms || i.perms.length === 0 || auth.can(...i.perms))
			}))
			.filter((g) => g.items.length > 0)
	);

	function active(href: string): boolean {
		const p = page.url.pathname;
		return href === '/' ? p === '/' : p === href || p.startsWith(href + '/');
	}
</script>

{#if mobileOpen}
	<button
		class="fixed inset-0 z-30 bg-black/50 lg:hidden"
		aria-label="Close navigation"
		onclick={() => (mobileOpen = false)}
	></button>
{/if}
<aside
	class="fixed inset-y-0 left-0 z-40 flex flex-col bg-sidebar text-sidebar-fg transition-all duration-200 lg:sticky lg:top-0 lg:h-screen lg:translate-x-0
		{collapsed ? 'lg:w-16' : 'lg:w-64'} w-64 {mobileOpen ? 'translate-x-0' : '-translate-x-full'}"
	aria-label="Main navigation"
>
	<div class="flex h-14 items-center gap-2.5 border-b border-white/5 px-4">
		<div
			class="flex size-8 shrink-0 items-center justify-center rounded-md bg-accent text-accent-fg"
		>
			<Cloud class="size-4.5" />
		</div>
		{#if !collapsed}
			<div class="min-w-0">
				<p class="truncate text-sm font-semibold text-white">{appConfig.name}</p>
				<p class="truncate text-[11px] text-sidebar-fg/70">Informatika &amp; Teknik Komputer</p>
			</div>
		{/if}
	</div>
	<nav class="flex-1 overflow-y-auto px-2 py-3">
		{#each groups as g (g.label)}
			<div class="mb-4">
				{#if !collapsed}<p
						class="mb-1 px-3 text-[11px] font-semibold tracking-wider text-sidebar-fg/50 uppercase"
					>
						{g.label}
					</p>{/if}
				<ul class="space-y-0.5">
					{#each g.items as item (item.href)}
						{@const Icon = item.icon}
						<li>
							<a
								href={item.href}
								class="flex items-center gap-3 rounded-md px-3 py-2 text-sm transition-colors {active(
									item.href
								)
									? 'bg-sidebar-active font-medium text-white'
									: 'hover:bg-sidebar-active/60 hover:text-white'}"
								aria-current={active(item.href) ? 'page' : undefined}
								title={collapsed ? item.label : undefined}
								onclick={() => (mobileOpen = false)}
							>
								<Icon class="size-4 shrink-0" aria-hidden="true" />
								{#if !collapsed}<span class="truncate">{item.label}</span>{:else}<span
										class="sr-only">{item.label}</span
									>{/if}
							</a>
						</li>
					{/each}
				</ul>
			</div>
		{/each}
	</nav>
	<button
		class="hidden items-center gap-2 border-t border-white/5 px-5 py-3 text-xs text-sidebar-fg/70 hover:text-white lg:flex"
		onclick={() => {
			collapsed = !collapsed;
			localStorage.setItem('itk-sidebar-collapsed', String(collapsed));
		}}
		aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'}
	>
		{#if collapsed}<PanelLeftOpen class="size-4" />{:else}<PanelLeftClose class="size-4" /> Collapse{/if}
	</button>
</aside>
