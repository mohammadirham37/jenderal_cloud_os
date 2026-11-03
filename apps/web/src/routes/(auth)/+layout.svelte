<script lang="ts">
	import { onMount } from 'svelte';
	import { appConfig } from '#lib/stores/config.svelte.ts';
	import { wallpaperById } from '#lib/desktop/wallpapers.ts';
	import type { LayoutProps } from './$types';

	// Lock-screen style: the wallpaper, a large clock and a frosted sign-in panel.
	let { children }: LayoutProps = $props();
	const wallpaper = wallpaperById('kontur');
	let now = $state(new Date());
	onMount(() => {
		const t = setInterval(() => (now = new Date()), 1000);
		return () => clearInterval(t);
	});
	const hh = $derived(String(now.getHours()).padStart(2, '0'));
	const mm = $derived(String(now.getMinutes()).padStart(2, '0'));
	const day = $derived(
		now.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' })
	);
</script>

<div
	class="dark relative min-h-screen overflow-hidden bg-ink text-fg"
	style="background:{wallpaper.css}"
>
	<div
		class="relative mx-auto flex min-h-screen max-w-6xl flex-col justify-between gap-10 px-6 py-8 lg:flex-row lg:items-center lg:px-10"
	>
		<section class="animate-rise text-white select-none lg:max-w-md" aria-label="Clock">
			<div class="mb-10 flex items-center gap-3 lg:mb-16">
				<span
					class="flex size-10 items-center justify-center rounded-xl bg-gradient-to-br from-signal to-[#c2410c] font-display text-base font-bold shadow-[0_0_28px_-4px_rgba(255,122,26,0.7)]"
					>IT</span
				>
				<div class="leading-tight">
					<p class="font-display text-lg font-semibold tracking-wide">{appConfig.name}</p>
					<p class="font-mono text-[11px] tracking-[0.18em] text-white/50 uppercase">
						Informatika · Teknik Komputer
					</p>
				</div>
			</div>
			<p
				class="font-display text-[96px] leading-none font-semibold tabular-nums [text-shadow:0_6px_40px_rgba(0,0,0,0.5)] sm:text-[132px]"
				aria-hidden="true"
			>
				{hh}<span class="animate-pulse text-signal">:</span>{mm}
			</p>
			<p class="mt-3 font-mono text-sm tracking-[0.3em] text-white/65 uppercase">{day}</p>
			<p class="mt-8 hidden max-w-sm text-sm leading-relaxed text-white/55 lg:block">
				Private cloud for laboratory workspaces, terminals, data and GPUs — one desktop for every
				student, lecturer and researcher.
			</p>
		</section>

		<main
			class="glass w-full max-w-md animate-pop rounded-2xl bg-glass-strong p-6 sm:p-8"
			style="animation-delay:120ms"
		>
			{@render children()}
		</main>
	</div>
	<p
		class="absolute bottom-4 left-1/2 -translate-x-1/2 text-center font-mono text-[10px] tracking-[0.2em] text-white/35 uppercase"
	>
		Authorised laboratory network users only · activity is logged
	</p>
</div>
