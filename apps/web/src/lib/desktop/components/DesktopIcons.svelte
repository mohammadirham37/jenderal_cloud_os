<script lang="ts">
	import { visibleApps } from '../apps';
	import { wm } from '../windows.svelte';
	import { auth } from '#lib/stores/auth.svelte.ts';

	let { light = false }: { light?: boolean } = $props();
	const icons = $derived(visibleApps((...p) => auth.can(...p)).filter((a) => a.desktop));
	let selected = $state<string | null>(null);
</script>

<ul
	class="absolute top-4 left-3 grid max-h-[calc(100%-6rem)] grid-flow-col grid-rows-[repeat(auto-fill,96px)] gap-x-1 gap-y-1"
	aria-label="Desktop apps"
>
	{#each icons as app, i (app.id)}
		{@const Icon = app.icon}
		<li class="animate-rise" style="animation-delay:{180 + i * 45}ms">
			<button
				class="group flex h-[92px] w-[88px] flex-col items-center gap-1.5 rounded-xl p-1.5 text-center transition outline-none
					{selected === app.id
					? light
						? 'bg-black/10 ring-1 ring-black/15'
						: 'bg-white/12 ring-1 ring-white/20'
					: light
						? 'hover:bg-black/6'
						: 'hover:bg-white/8'}
					focus-visible:ring-2 focus-visible:ring-signal"
				onclick={() => {
					selected = app.id;
					if (wm.compact) wm.open(app.id);
				}}
				ondblclick={() => wm.open(app.id)}
				onkeydown={(e) => e.key === 'Enter' && wm.open(app.id)}
				aria-label="Open {app.title}"
			>
				<span
					class="relative flex size-12 items-center justify-center rounded-[14px] text-white shadow-[0_10px_24px_-8px_rgba(0,0,0,0.6)] transition group-hover:-translate-y-0.5 group-active:scale-95"
					style="background:linear-gradient(150deg,{app.tint},color-mix(in srgb,{app.tint} 55%,black))"
				>
					<span
						class="absolute inset-0 rounded-[14px] bg-gradient-to-b from-white/25 to-transparent opacity-60"
					></span>
					<Icon class="relative size-6" />
				</span>
				<span
					class="line-clamp-2 text-[12px] leading-tight font-medium {light
						? 'text-[#1b2a3a]'
						: 'text-white [text-shadow:0_1px_3px_rgba(0,0,0,0.85)]'}">{app.title}</span
				>
			</button>
		</li>
	{/each}
</ul>
