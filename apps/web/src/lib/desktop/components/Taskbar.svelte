<script lang="ts">
	import { Bell, Search } from '@lucide/svelte';
	import Logo from '#lib/components/brand/Logo.svelte';
	import { appById, visibleApps } from '../apps';
	import { wm } from '../windows.svelte';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { notificationCounter } from '#lib/stores/notifications.svelte.ts';

	let {
		startOpen = $bindable(false),
		notifOpen = $bindable(false),
		server
	}: {
		startOpen?: boolean;
		notifOpen?: boolean;
		server: 'up' | 'degraded' | 'down' | 'unknown';
	} = $props();

	// Pinned apps (desktop apps) first, then running apps that are not pinned.
	const pinned = $derived(
		visibleApps((...p) => auth.can(...p))
			.filter((a) => a.desktop)
			.slice(0, 7)
	);
	const running = $derived(wm.windows.filter((w) => !pinned.some((p) => p.id === w.appId)));

	let now = $state(new Date());
	$effect(() => {
		const t = setInterval(() => (now = new Date()), 10_000);
		return () => clearInterval(t);
	});
	const time = $derived(now.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }));
	const date = $derived(
		now.toLocaleDateString([], { weekday: 'short', day: 'numeric', month: 'short' })
	);

	function clickApp(appId: string) {
		const wins = wm.windows.filter((w) => w.appId === appId);
		if (wins.length === 0) wm.open(appId);
		else if (wins.length === 1) wm.toggleFromTaskbar(wins[0].id);
		else {
			// Cycle through the app's windows.
			const sorted = [...wins].sort((a, b) => b.z - a.z);
			const next = wm.focusedId === sorted[0].id ? sorted[sorted.length - 1] : sorted[0];
			wm.focus(next.id);
		}
	}
	function appState(appId: string): 'none' | 'open' | 'focused' {
		const wins = wm.windows.filter((w) => w.appId === appId);
		if (wins.length === 0) return 'none';
		return wins.some((w) => w.id === wm.focusedId && !w.minimized) ? 'focused' : 'open';
	}
	const statusColor = {
		up: 'bg-emerald-400',
		degraded: 'bg-amber-400',
		down: 'bg-red-500',
		unknown: 'bg-white/40'
	};
	const statusLabel = {
		up: 'All systems operational',
		degraded: 'Some services degraded',
		down: 'Service outage',
		unknown: 'Status unknown'
	};
</script>

<nav
	class="glass fixed inset-x-2 bottom-2 z-[9000] flex h-13 animate-rise items-center gap-1.5 rounded-2xl px-2 text-[#dfe6ee] sm:inset-x-3"
	style="animation-delay: 120ms"
	aria-label="Taskbar"
>
	<button
		class="group flex h-10 items-center gap-2 rounded-xl px-2.5 transition hover:bg-white/10 {startOpen
			? 'bg-white/12'
			: ''}"
		onclick={() => {
			startOpen = !startOpen;
			notifOpen = false;
		}}
		aria-haspopup="dialog"
		aria-expanded={startOpen}
		aria-label="Start"
	>
		<span
			class="relative flex size-7 items-center justify-center drop-shadow-[0_0_10px_rgba(255,122,26,0.55)] transition group-hover:drop-shadow-[0_0_14px_rgba(255,122,26,0.8)]"
		>
			<Logo size={26} label={null} />
			<span
				class="absolute -right-0.5 -bottom-0.5 size-2 rounded-full border border-[#111821] bg-emerald-400"
			></span>
		</span>
		<span class="hidden font-display text-sm font-semibold tracking-wide sm:inline">Jenderal</span>
	</button>
	<button
		class="hidden h-9 w-52 items-center gap-2 rounded-xl bg-white/6 px-3 text-left text-sm text-white/45 ring-1 ring-white/8 transition hover:bg-white/10 lg:flex"
		onclick={() => (startOpen = true)}
		aria-label="Search apps"
	>
		<Search class="size-4" /> Search apps…
	</button>

	<div class="mx-1 h-7 w-px bg-white/10" aria-hidden="true"></div>

	<ul class="flex min-w-0 flex-1 items-center gap-1 overflow-x-auto" role="list">
		{#each pinned as app (app.id)}
			{@const Icon = app.icon}
			{@const st = appState(app.id)}
			<li>
				<button
					class="group relative flex size-10 shrink-0 items-center justify-center rounded-xl transition hover:bg-white/10 {st ===
					'focused'
						? 'bg-white/12'
						: ''}"
					onclick={() => clickApp(app.id)}
					title={app.title}
					aria-label="{app.title}{st !== 'none' ? ' (open)' : ''}"
					aria-pressed={st === 'focused'}
				>
					<span
						class="flex size-7 items-center justify-center rounded-lg text-white transition group-hover:scale-110 group-active:scale-95"
						style="background:{app.tint}"
					>
						<Icon class="size-4" />
					</span>
					<span
						class="absolute bottom-0.5 h-[3px] rounded-full transition-all {st === 'focused'
							? 'w-4 bg-signal'
							: st === 'open'
								? 'w-1.5 bg-white/60'
								: 'w-0'}"
					></span>
				</button>
			</li>
		{/each}
		{#each running as w (w.id)}
			{@const app = appById(w.appId)!}
			{@const Icon = app.icon}
			<li>
				<button
					class="group relative flex h-10 max-w-48 shrink-0 items-center gap-2 rounded-xl pr-3 pl-1.5 text-sm transition hover:bg-white/10 {wm.focusedId ===
						w.id && !w.minimized
						? 'bg-white/12'
						: ''}"
					onclick={() => wm.toggleFromTaskbar(w.id)}
					aria-label="{w.title} (open)"
				>
					<span
						class="flex size-7 shrink-0 items-center justify-center rounded-lg text-white"
						style="background:{app.tint}"
					>
						<Icon class="size-4" />
					</span>
					<span class="hidden truncate md:inline">{w.title}</span>
					<span
						class="absolute bottom-0.5 left-1/2 h-[3px] -translate-x-1/2 rounded-full {wm.focusedId ===
							w.id && !w.minimized
							? 'w-4 bg-signal'
							: 'w-1.5 bg-white/60'}"
					></span>
				</button>
			</li>
		{/each}
	</ul>

	<div class="flex items-center gap-0.5">
		<span
			class="hidden items-center gap-2 rounded-lg px-2.5 py-1.5 text-xs text-white/60 xl:flex"
			title={statusLabel[server]}
		>
			<span class="relative flex size-2">
				{#if server === 'up'}<span
						class="absolute inline-flex size-full animate-ping rounded-full bg-emerald-400 opacity-40"
					></span>{/if}
				<span class="relative inline-flex size-2 rounded-full {statusColor[server]}"></span>
			</span>
			{statusLabel[server]}
		</span>
		<span
			class="flex size-2 rounded-full xl:hidden {statusColor[server]}"
			title={statusLabel[server]}
			role="img"
			aria-label={statusLabel[server]}
		></span>
		<button
			class="relative flex size-10 items-center justify-center rounded-xl transition hover:bg-white/10 {notifOpen
				? 'bg-white/12'
				: ''}"
			onclick={() => {
				notifOpen = !notifOpen;
				startOpen = false;
			}}
			aria-label="Notifications ({notificationCounter.unread} unread)"
			aria-expanded={notifOpen}
		>
			<Bell class="size-[18px]" />
			{#if notificationCounter.unread > 0}
				<span
					class="absolute top-1.5 right-1.5 min-w-4 rounded-full bg-signal px-1 text-center font-mono text-[10px] leading-4 font-medium text-[#1a0d03]"
				>
					{notificationCounter.unread > 99 ? '99+' : notificationCounter.unread}
				</span>
			{/if}
		</button>
		<div class="hidden flex-col items-end px-2.5 leading-tight sm:flex" aria-label="Clock">
			<span class="font-display text-[15px] font-semibold tabular-nums">{time}</span>
			<span class="font-mono text-[10px] tracking-wide text-white/50 uppercase">{date}</span>
		</div>
	</div>
</nav>
