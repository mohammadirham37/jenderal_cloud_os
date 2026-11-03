<script lang="ts">
	import { BellOff, CheckCheck } from '@lucide/svelte';
	import { appForPath } from '../apps';
	import { wm } from '../windows.svelte';
	import { api } from '#lib/api/client.ts';
	import type { Notification } from '#lib/api/types.ts';
	import { notificationCounter } from '#lib/stores/notifications.svelte.ts';
	import { relative } from '#lib/format.ts';

	let { open = $bindable(false) }: { open?: boolean } = $props();
	let items = $state<Notification[] | null>(null);
	let failed = $state(false);

	async function load() {
		try {
			const r = await api.list<Notification>('/notifications', { per_page: 8 });
			items = r.data;
			failed = false;
		} catch {
			failed = true;
		}
	}
	$effect(() => {
		if (open) load();
	});
	async function openItem(n: Notification) {
		if (!n.read_at) {
			await api.post(`/notifications/${n.id}/read`).catch(() => {});
			notificationCounter.refresh();
		}
		const app = n.link ? appForPath(n.link) : undefined;
		wm.open(app?.id ?? 'notifications', app ? n.link! : undefined);
		open = false;
	}
	async function readAll() {
		await api.post('/notifications/read-all').catch(() => {});
		notificationCounter.refresh();
		load();
	}
	const dot = {
		info: 'bg-sky-400',
		success: 'bg-emerald-400',
		warning: 'bg-amber-400',
		critical: 'bg-red-500'
	};
</script>

{#if open}
	<button
		class="fixed inset-0 z-[9050] cursor-default"
		aria-label="Close notifications"
		onclick={() => (open = false)}
	></button>
	<div
		class="glass fixed right-2 bottom-17 z-[9100] flex max-h-[min(560px,calc(100vh-6rem))] w-[min(380px,calc(100vw-1rem))] animate-pop flex-col rounded-2xl bg-glass-strong text-[#dfe6ee] sm:right-3"
		role="dialog"
		aria-label="Notifications"
		tabindex="-1"
		onkeydown={(e) => e.key === 'Escape' && (open = false)}
	>
		<header class="flex items-center justify-between border-b border-white/8 px-4 py-3">
			<h2 class="font-display text-sm font-semibold tracking-wide">Notifications</h2>
			<button
				class="flex items-center gap-1 rounded-md px-2 py-1 text-xs text-white/60 hover:bg-white/10 hover:text-white"
				onclick={readAll}
			>
				<CheckCheck class="size-3.5" /> Mark all read
			</button>
		</header>
		<div class="min-h-0 flex-1 overflow-y-auto p-2">
			{#if failed}
				<p class="p-6 text-center text-sm text-white/50">Notifications could not be loaded.</p>
			{:else if items === null}
				<div class="space-y-2 p-2">
					{#each [1, 2, 3] as i (i)}<div
							class="h-12 animate-pulse rounded-lg bg-white/6"
						></div>{/each}
				</div>
			{:else if items.length === 0}
				<div class="flex flex-col items-center gap-2 p-8 text-white/45">
					<BellOff class="size-6" />
					<p class="text-sm">You're all caught up.</p>
				</div>
			{:else}
				<ul>
					{#each items as n (n.id)}
						<li>
							<button
								class="flex w-full gap-3 rounded-xl p-2.5 text-left transition hover:bg-white/8"
								onclick={() => openItem(n)}
							>
								<span
									class="mt-1.5 size-2 shrink-0 rounded-full {n.read_at
										? 'bg-white/15'
										: dot[n.severity]}"
								></span>
								<span class="min-w-0 flex-1">
									<span
										class="block truncate text-sm {n.read_at
											? 'text-white/60'
											: 'font-medium text-white'}">{n.title}</span
									>
									{#if n.body}<span class="line-clamp-2 text-xs text-white/50">{n.body}</span>{/if}
									<span class="mt-0.5 block font-mono text-[10px] text-white/35 uppercase"
										>{n.type} · {relative(n.created_at)}</span
									>
								</span>
							</button>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
		<button
			class="border-t border-white/8 px-4 py-2.5 text-sm text-signal hover:bg-white/5"
			onclick={() => {
				wm.open('notifications');
				open = false;
			}}
		>
			Open notification center
		</button>
	</div>
{/if}
