<script lang="ts">
	import { goto } from '$app/navigation';
	import { Bell, Menu, Moon, Search, Sun, Monitor, LogOut, User } from '@lucide/svelte';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { theme } from '#lib/stores/theme.svelte.ts';
	import { notificationCounter } from '#lib/stores/notifications.svelte.ts';
	import { api } from '#lib/api/client.ts';
	import Breadcrumbs from './Breadcrumbs.svelte';

	let { onmenu }: { onmenu: () => void } = $props();
	let q = $state('');
	let menuOpen = $state(false);
	let server = $state<'up' | 'degraded' | 'down' | 'unknown'>('unknown');

	async function checkServer() {
		if (!auth.can('monitoring.view_host', 'host.operations.view')) {
			try {
				await fetch('/health/live');
				server = 'up';
			} catch {
				server = 'down';
			}
			return;
		}
		try {
			const r = await api.get<{ services: { name: string; status: string }[] }>(
				'/monitoring/services'
			);
			const bad = r.services.filter((s) => s.status === 'down');
			const deg = r.services.filter((s) => s.status === 'degraded');
			server = bad.length ? 'down' : deg.length ? 'degraded' : 'up';
		} catch {
			server = 'unknown';
		}
	}

	$effect(() => {
		checkServer();
		const t = setInterval(checkServer, 60000);
		return () => clearInterval(t);
	});

	function search(e: SubmitEvent) {
		e.preventDefault();
		if (q.trim()) goto(`/workspaces?q=${encodeURIComponent(q.trim())}`);
	}
	const serverLabel = {
		up: 'All systems operational',
		degraded: 'Some services degraded',
		down: 'Service outage',
		unknown: 'Status unknown'
	};
	const serverColor = {
		up: 'bg-success',
		degraded: 'bg-warning',
		down: 'bg-danger',
		unknown: 'bg-fg-subtle'
	};
	const themes = [
		{ v: 'light' as const, icon: Sun, label: 'Light' },
		{ v: 'dark' as const, icon: Moon, label: 'Dark' },
		{ v: 'system' as const, icon: Monitor, label: 'System' }
	];
</script>

<header
	class="sticky top-0 z-20 flex h-14 items-center gap-3 border-b border-border bg-panel/90 px-4 backdrop-blur lg:px-6"
>
	<button
		class="rounded p-1.5 text-fg-muted hover:bg-panel-2 lg:hidden"
		onclick={onmenu}
		aria-label="Open navigation"><Menu class="size-5" /></button
	>
	<div class="hidden min-w-0 flex-1 md:block"><Breadcrumbs /></div>
	<div class="flex-1 md:hidden"></div>
	<form class="relative hidden w-64 sm:block" onsubmit={search} role="search">
		<Search
			class="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-fg-subtle"
			aria-hidden="true"
		/>
		<input
			class="input h-8 pl-8"
			placeholder="Search workspaces…"
			bind:value={q}
			aria-label="Search workspaces"
		/>
	</form>
	<a
		href={auth.can('monitoring.view_host') ? '/monitoring?tab=services' : undefined}
		class="hidden items-center gap-1.5 rounded-md px-2 py-1 text-xs text-fg-muted lg:flex"
		title={serverLabel[server]}
	>
		<span class="size-2 rounded-full {serverColor[server]}" aria-hidden="true"></span>
		<span>{serverLabel[server]}</span>
	</a>
	<a
		href="/notifications"
		class="relative rounded p-1.5 text-fg-muted hover:bg-panel-2 hover:text-fg"
		aria-label="Notifications ({notificationCounter.unread} unread)"
	>
		<Bell class="size-5" />
		{#if notificationCounter.unread > 0}
			<span
				class="absolute -top-0.5 -right-0.5 min-w-4 rounded-full bg-danger px-1 text-center text-[10px] leading-4 font-semibold text-white"
			>
				{notificationCounter.unread > 99 ? '99+' : notificationCounter.unread}
			</span>
		{/if}
	</a>
	<div class="relative">
		<button
			class="flex items-center gap-2 rounded-md p-1 pr-2 hover:bg-panel-2"
			onclick={() => (menuOpen = !menuOpen)}
			aria-haspopup="menu"
			aria-expanded={menuOpen}
		>
			<span
				class="flex size-7 items-center justify-center rounded-full bg-accent-soft text-xs font-semibold text-accent uppercase"
			>
				{auth.me?.username.slice(0, 2)}
			</span>
			<span class="hidden text-sm font-medium sm:inline">{auth.me?.username}</span>
		</button>
		{#if menuOpen}
			<button
				class="fixed inset-0 z-10 cursor-default"
				aria-label="Close menu"
				onclick={() => (menuOpen = false)}
			></button>
			<div class="card absolute right-0 z-20 mt-2 w-60 p-1.5 shadow-lg" role="menu">
				<div class="border-b border-border px-3 py-2">
					<p class="truncate text-sm font-medium">{auth.me?.full_name}</p>
					<p class="truncate text-xs text-fg-muted">{auth.me?.roles.join(', ')}</p>
				</div>
				<a
					href="/account"
					class="mt-1 flex items-center gap-2 rounded px-3 py-2 text-sm hover:bg-panel-2"
					role="menuitem"
					onclick={() => (menuOpen = false)}
				>
					<User class="size-4" /> My account
				</a>
				<div class="flex items-center gap-1 px-3 py-2" role="group" aria-label="Theme">
					{#each themes as t (t.v)}
						{@const Icon = t.icon}
						<button
							class="flex flex-1 items-center justify-center gap-1 rounded py-1 text-xs {theme.value ===
							t.v
								? 'bg-accent-soft text-accent'
								: 'text-fg-muted hover:bg-panel-2'}"
							onclick={() => theme.set(t.v)}
							aria-pressed={theme.value === t.v}
						>
							<Icon class="size-3.5" />{t.label}
						</button>
					{/each}
				</div>
				<button
					class="flex w-full items-center gap-2 rounded px-3 py-2 text-sm text-danger hover:bg-danger-soft"
					role="menuitem"
					onclick={() => auth.logout()}
				>
					<LogOut class="size-4" /> Sign out
				</button>
			</div>
		{/if}
	</div>
</header>
