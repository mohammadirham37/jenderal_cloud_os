<script lang="ts">
	import { Eye, LayoutPanelLeft, Plus, RefreshCw, SquareTerminal } from '@lucide/svelte';
	import { wallpapers } from '../wallpapers';
	import { wm } from '../windows.svelte';
	import { auth } from '#lib/stores/auth.svelte.ts';

	let { at = $bindable(null) }: { at?: { x: number; y: number } | null } = $props();
	let menu = $state<HTMLDivElement>();

	const pos = $derived.by(() => {
		if (!at) return { x: 0, y: 0 };
		return { x: Math.min(at.x, wm.viewport.w - 248), y: Math.min(at.y, wm.viewport.h - 330) };
	});
	$effect(() => {
		if (at) queueMicrotask(() => menu?.querySelector<HTMLElement>('[role=menuitem]')?.focus());
	});
	function run(fn: () => void) {
		fn();
		at = null;
	}
	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') at = null;
		if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
		e.preventDefault();
		const items = [...(menu?.querySelectorAll<HTMLElement>('[role=menuitem]') ?? [])];
		const i = items.indexOf(document.activeElement as HTMLElement);
		items[(i + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length]?.focus();
	}
	const item =
		'flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left text-sm text-white/85 transition hover:bg-white/10 focus:bg-white/10 focus:outline-none';
</script>

{#if at}
	<button
		class="fixed inset-0 z-[9400] cursor-default"
		aria-label="Close menu"
		onclick={() => (at = null)}
		oncontextmenu={(e) => {
			e.preventDefault();
			at = null;
		}}
	></button>
	<div
		bind:this={menu}
		class="glass fixed z-[9500] w-60 animate-pop rounded-xl p-1.5"
		style="left:{pos.x}px; top:{pos.y}px"
		role="menu"
		tabindex="-1"
		aria-label="Desktop menu"
		{onkeydown}
	>
		{#if auth.can('terminals.open')}
			<button role="menuitem" class={item} onclick={() => run(() => wm.open('terminal'))}
				><SquareTerminal class="size-4 text-white/60" /> Open Terminal</button
			>
		{/if}
		{#if auth.can('workspaces.create')}
			<button
				role="menuitem"
				class={item}
				onclick={() => run(() => wm.open('workspaces', '/workspaces/new'))}
				><Plus class="size-4 text-white/60" /> New workspace</button
			>
		{/if}
		<button role="menuitem" class={item} onclick={() => run(() => wm.showDesktop())}
			><Eye class="size-4 text-white/60" /> Show desktop</button
		>
		<button role="menuitem" class={item} onclick={() => run(() => location.reload())}
			><RefreshCw class="size-4 text-white/60" /> Refresh</button
		>
		<div class="my-1.5 h-px bg-white/10"></div>
		<p
			class="px-2.5 pt-1 pb-1.5 font-display text-[10px] font-semibold tracking-[0.2em] text-white/45 uppercase"
		>
			Wallpaper
		</p>
		<div class="grid grid-cols-4 gap-1.5 px-2 pb-2">
			{#each wallpapers as w (w.id)}
				<button
					role="menuitem"
					class="aspect-[4/3] rounded-md ring-2 {wm.wallpaper === w.id
						? 'ring-signal'
						: 'ring-white/10 hover:ring-white/40'}"
					style="background:{w.swatch}"
					title={w.name}
					aria-label="Wallpaper {w.name}"
					onclick={() => run(() => wm.setWallpaper(w.id))}
				></button>
			{/each}
		</div>
		<div class="my-1 h-px bg-white/10"></div>
		<a role="menuitem" href="/dashboard" class={item}
			><LayoutPanelLeft class="size-4 text-white/60" /> Classic console</a
		>
	</div>
{/if}
