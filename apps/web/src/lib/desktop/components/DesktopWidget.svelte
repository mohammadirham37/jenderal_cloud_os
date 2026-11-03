<script lang="ts">
	import { api } from '#lib/api/client.ts';
	import type { Announcement } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { appConfig } from '#lib/stores/config.svelte.ts';

	// Large clock + greeting + running workloads, printed straight on the wallpaper.
	let { light = false }: { light?: boolean } = $props();
	let now = $state(new Date());
	let running = $state<number | null>(null);
	let announcement = $state<Announcement | null>(null);

	$effect(() => {
		const t = setInterval(() => (now = new Date()), 1000);
		const load = () =>
			api
				.get<{ workspaces_by_status: Record<string, number> }>('/dashboard')
				.then((d) => (running = d.workspaces_by_status.running ?? 0))
				.catch(() => (running = null));
		load();
		api
			.get<Announcement[]>('/announcements')
			.then((a) => (announcement = a[0] ?? null))
			.catch(() => {});
		const t2 = setInterval(load, 30_000);
		return () => {
			clearInterval(t);
			clearInterval(t2);
		};
	});
	const hh = $derived(String(now.getHours()).padStart(2, '0'));
	const mm = $derived(String(now.getMinutes()).padStart(2, '0'));
	const day = $derived(
		now.toLocaleDateString(undefined, {
			weekday: 'long',
			day: 'numeric',
			month: 'long',
			year: 'numeric'
		})
	);
	const greeting = $derived(
		now.getHours() < 11
			? 'Selamat pagi'
			: now.getHours() < 15
				? 'Selamat siang'
				: now.getHours() < 18
					? 'Selamat sore'
					: 'Selamat malam'
	);
	const first = $derived(auth.me?.full_name.split(' ')[0] ?? '');
</script>

<div
	class="pointer-events-none absolute top-6 right-8 hidden max-w-sm text-right select-none md:block {light
		? 'text-[#14202c]'
		: 'text-white'}"
	aria-hidden="true"
>
	<p
		class="animate-rise font-display text-[88px] leading-none font-semibold tracking-tight tabular-nums {light
			? ''
			: '[text-shadow:0_4px_30px_rgba(0,0,0,0.45)]'}"
	>
		{hh}<span class="animate-pulse text-signal">:</span>{mm}
	</p>
	<p
		class="mt-2 animate-rise font-mono text-xs tracking-[0.25em] uppercase opacity-70"
		style="animation-delay:80ms"
	>
		{day}
	</p>
	<p
		class="mt-5 animate-rise text-lg {light ? '' : '[text-shadow:0_1px_8px_rgba(0,0,0,0.6)]'}"
		style="animation-delay:160ms"
	>
		{greeting}, <span class="font-semibold">{first}</span>.
	</p>
	<p class="mt-1 animate-rise text-sm opacity-70" style="animation-delay:220ms">
		{appConfig.name}{#if running !== null}
			· {running} workspace{running === 1 ? '' : 's'} running{/if}
	</p>
	{#if announcement}
		<div
			class="glass pointer-events-auto mt-5 animate-rise rounded-xl p-3 text-left text-[#dfe6ee]"
			style="animation-delay:300ms"
		>
			<p class="font-display text-[10px] font-semibold tracking-[0.2em] text-signal uppercase">
				Announcement
			</p>
			<p class="mt-1 text-sm font-medium text-white">{announcement.title}</p>
			<p class="mt-0.5 line-clamp-3 text-xs text-white/60">{announcement.body}</p>
		</div>
	{/if}
</div>
