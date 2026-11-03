<script lang="ts">
	import { onMount } from 'svelte';
	import AppWindow from '#lib/desktop/components/AppWindow.svelte';
	import ContextMenu from '#lib/desktop/components/ContextMenu.svelte';
	import DesktopIcons from '#lib/desktop/components/DesktopIcons.svelte';
	import DesktopWidget from '#lib/desktop/components/DesktopWidget.svelte';
	import NotificationFlyout from '#lib/desktop/components/NotificationFlyout.svelte';
	import StartMenu from '#lib/desktop/components/StartMenu.svelte';
	import Taskbar from '#lib/desktop/components/Taskbar.svelte';
	import { appById, parseWindowName } from '#lib/desktop/apps.ts';
	import type { ShellMessage } from '#lib/desktop/embed.ts';
	import { wallpaperById } from '#lib/desktop/wallpapers.ts';
	import { wm } from '#lib/desktop/windows.svelte.ts';
	import { api } from '#lib/api/client.ts';
	import type { Notification } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { appConfig } from '#lib/stores/config.svelte.ts';
	import { notificationCounter } from '#lib/stores/notifications.svelte.ts';
	import { toast } from '#lib/stores/toast.svelte.ts';

	let startOpen = $state(false);
	let notifOpen = $state(false);
	let menuAt = $state<{ x: number; y: number } | null>(null);
	let server = $state<'up' | 'degraded' | 'down' | 'unknown'>('unknown');
	const wallpaper = $derived(wallpaperById(wm.wallpaper));

	function measure() {
		wm.viewport = { w: window.innerWidth, h: window.innerHeight };
	}

	async function checkServer() {
		if (!auth.can('monitoring.view_host', 'host.operations.view')) {
			server = (await fetch('/health/live')
				.then((r) => r.ok)
				.catch(() => false))
				? 'up'
				: 'down';
			return;
		}
		try {
			const r = await api.get<{ services: { status: string }[] }>('/monitoring/services');
			server = r.services.some((s) => s.status === 'down')
				? 'down'
				: r.services.some((s) => s.status === 'degraded')
					? 'degraded'
					: 'up';
		} catch {
			server = 'unknown';
		}
	}

	/** Finds the window whose iframe sent a message. */
	function windowOf(source: MessageEventSource | null) {
		const frames = document.querySelectorAll<HTMLIFrameElement>('iframe[name^="itk-win:"]');
		for (const f of frames) {
			if (f.contentWindow === source) return parseWindowName(f.name)?.windowId ?? null;
		}
		return null;
	}

	function onMessage(e: MessageEvent<ShellMessage>) {
		if (e.origin !== location.origin || !e.data || typeof e.data !== 'object') return;
		const id = windowOf(e.source);
		if (!id) return;
		const msg = e.data;
		if (msg.type === 'itk:nav') wm.report(id, msg.title, msg.path);
		else if (msg.type === 'itk:open' && appById(msg.appId) && msg.url.startsWith('/'))
			wm.open(msg.appId, msg.url);
	}

	// Clicking inside a window's iframe does not bubble to this document:
	// detect it through the blur that moves focus into the iframe.
	function onBlur() {
		setTimeout(() => {
			const el = document.activeElement;
			if (el instanceof HTMLIFrameElement) {
				const id = parseWindowName(el.name)?.windowId;
				if (id && wm.focusedId !== id) wm.focus(id);
			}
		});
	}

	function onKey(e: KeyboardEvent) {
		if (e.key === 'Escape') {
			startOpen = false;
			notifOpen = false;
			menuAt = null;
		}
		// Ctrl+Alt+T opens a terminal, Ctrl+Alt+D shows the desktop.
		if (e.ctrlKey && e.altKey && e.key.toLowerCase() === 't' && auth.can('terminals.open')) {
			e.preventDefault();
			wm.open('terminal');
		}
		if (e.ctrlKey && e.altKey && e.key.toLowerCase() === 'd') {
			e.preventDefault();
			wm.showDesktop();
		}
	}

	// Pop a toast when new notifications arrive.
	let lastUnread = -1;
	$effect(() => {
		const n = notificationCounter.unread;
		if (lastUnread >= 0 && n > lastUnread) {
			api
				.list<Notification>('/notifications', { per_page: 1, unread: 'true' })
				.then((r) => r.data[0] && toast.info(r.data[0].title, r.data[0].body || undefined))
				.catch(() => {});
		}
		lastUnread = n;
	});

	onMount(() => {
		measure();
		wm.load(auth.me!.id);
		appConfig.load();
		notificationCounter.start();
		checkServer();
		const t = setInterval(checkServer, 60_000);
		window.addEventListener('resize', measure);
		window.addEventListener('message', onMessage);
		window.addEventListener('blur', onBlur);
		if (wm.windows.length === 0 && !sessionStorage.getItem('itk-welcomed')) {
			sessionStorage.setItem('itk-welcomed', '1');
			toast.success(
				`Welcome, ${auth.me!.full_name.split(' ')[0]}`,
				'Double-click an icon or press Start to open an app.'
			);
		}
		return () => {
			clearInterval(t);
			notificationCounter.stop();
			window.removeEventListener('resize', measure);
			window.removeEventListener('message', onMessage);
			window.removeEventListener('blur', onBlur);
		};
	});

	const snapRect = $derived.by(() => {
		const vw = wm.viewport.w;
		const vh = wm.viewport.h - 64;
		if (wm.snapPreview === 'left') return { x: 8, y: 8, w: vw / 2 - 12, h: vh - 16 };
		if (wm.snapPreview === 'right') return { x: vw / 2 + 4, y: 8, w: vw / 2 - 12, h: vh - 16 };
		if (wm.snapPreview === 'max') return { x: 8, y: 8, w: vw - 16, h: vh - 16 };
		return null;
	});
</script>

<svelte:head><title>{appConfig.name} · Desktop</title></svelte:head>
<svelte:window onkeydown={onKey} />

<div
	class="dark fixed inset-0 overflow-hidden bg-ink font-sans text-fg select-none"
	style="background:{wallpaper.css}"
>
	<main
		id="main"
		class="absolute inset-0"
		aria-label="Desktop"
		oncontextmenu={(e) => {
			if ((e.target as Element).closest('[data-window], nav, [role=dialog]')) return;
			e.preventDefault();
			menuAt = { x: e.clientX, y: e.clientY };
		}}
	>
		<h1 class="sr-only">{appConfig.name} desktop</h1>
		<DesktopWidget light={wallpaper.light} />
		<DesktopIcons light={wallpaper.light} />

		{#if snapRect}
			<div
				class="pointer-events-none absolute z-[8000] rounded-xl border-2 border-signal/70 bg-signal/12 backdrop-blur-sm transition-all duration-150"
				style="left:{snapRect.x}px; top:{snapRect.y}px; width:{snapRect.w}px; height:{snapRect.h}px"
			></div>
		{/if}

		{#each wm.windows as win (win.id)}
			<AppWindow {win} />
		{/each}
	</main>

	<Taskbar bind:startOpen bind:notifOpen {server} />
	<StartMenu bind:open={startOpen} />
	<NotificationFlyout bind:open={notifOpen} />
	<ContextMenu bind:at={menuAt} />
</div>
