<script lang="ts">
	import { LayoutPanelLeft, LogOut, Monitor, Moon, Search, Sun } from '@lucide/svelte';
	import { categories, visibleApps, type DesktopApp } from '../apps';
	import { wallpapers } from '../wallpapers';
	import { wm } from '../windows.svelte';
	import { api } from '#lib/api/client.ts';
	import type { Quota, Usage } from '#lib/api/types.ts';
	import { auth } from '#lib/stores/auth.svelte.ts';
	import { theme } from '#lib/stores/theme.svelte.ts';
	import { bytes, cores } from '#lib/format.ts';

	let { open = $bindable(false) }: { open?: boolean } = $props();

	let q = $state('');
	let input = $state<HTMLInputElement>();
	let usage = $state<{ quota: Quota; usage: Usage } | null>(null);

	const all = $derived(visibleApps((...p) => auth.can(...p)));
	const filtered = $derived(
		q.trim()
			? all.filter((a) =>
					`${a.title} ${a.blurb} ${a.category}`.toLowerCase().includes(q.trim().toLowerCase())
				)
			: all
	);

	$effect(() => {
		if (!open) return;
		q = '';
		queueMicrotask(() => input?.focus());
		api
			.get<{ quota: Quota; usage: Usage }>('/me/usage')
			.then((u) => (usage = u))
			.catch(() => (usage = null));
	});

	function launch(app: DesktopApp) {
		wm.open(app.id);
		open = false;
	}
	function onkeydown(e: KeyboardEvent) {
		if (e.key === 'Escape') open = false;
		if (e.key === 'Enter' && filtered[0] && document.activeElement === input) launch(filtered[0]);
	}
	const meters = $derived(
		usage
			? [
					{
						label: 'CPU',
						v: usage.usage.running_cpu_millis,
						max: usage.quota.cpu_millis,
						text: `${cores(usage.usage.running_cpu_millis)} / ${cores(usage.quota.cpu_millis)}`
					},
					{
						label: 'Memory',
						v: usage.usage.running_memory_bytes,
						max: usage.quota.memory_bytes,
						text: `${bytes(usage.usage.running_memory_bytes)} / ${bytes(usage.quota.memory_bytes)}`
					},
					{
						label: 'Storage',
						v: usage.usage.allocated_storage_bytes,
						max: usage.quota.storage_bytes,
						text: `${bytes(usage.usage.allocated_storage_bytes)} / ${bytes(usage.quota.storage_bytes)}`
					}
				]
			: []
	);
	const initials = $derived(
		(auth.me?.full_name ?? '?')
			.split(/\s+/)
			.map((s) => s[0])
			.slice(0, 2)
			.join('')
			.toUpperCase()
	);
</script>

{#if open}
	<button
		class="fixed inset-0 z-[9050] cursor-default"
		aria-label="Close start menu"
		onclick={() => (open = false)}
	></button>
	<div
		class="glass fixed bottom-17 left-2 z-[9100] flex max-h-[min(640px,calc(100vh-6rem))] w-[min(760px,calc(100vw-1rem))] animate-pop overflow-hidden rounded-2xl bg-glass-strong text-[#dfe6ee] sm:left-3"
		role="dialog"
		aria-label="Start menu"
		tabindex="-1"
		{onkeydown}
	>
		<div class="flex min-w-0 flex-1 flex-col">
			<div class="p-4 pb-2">
				<label class="relative block">
					<span class="sr-only">Search apps</span>
					<Search
						class="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-white/40"
					/>
					<input
						bind:this={input}
						bind:value={q}
						class="h-10 w-full rounded-xl border-0 bg-white/8 pl-9 text-sm text-white ring-1 ring-white/10 placeholder:text-white/35 focus:bg-white/12 focus:ring-2 focus:ring-signal"
						placeholder="Type to search apps…"
						autocomplete="off"
					/>
				</label>
			</div>
			<div class="min-h-0 flex-1 overflow-y-auto px-4 pb-4">
				{#each categories as cat (cat)}
					{@const list = filtered.filter((a) => a.category === cat)}
					{#if list.length}
						<p
							class="mt-3 mb-2 font-display text-[11px] font-semibold tracking-[0.2em] text-signal uppercase"
						>
							{cat}
						</p>
						<ul class="grid grid-cols-2 gap-1.5 sm:grid-cols-3">
							{#each list as app, i (app.id)}
								{@const Icon = app.icon}
								<li class="animate-rise" style="animation-delay:{i * 18}ms">
									<button
										class="group flex w-full items-center gap-3 rounded-xl p-2 text-left transition hover:bg-white/8 focus-visible:bg-white/10"
										onclick={() => launch(app)}
									>
										<span
											class="flex size-9 shrink-0 items-center justify-center rounded-xl text-white shadow-md transition group-hover:scale-105"
											style="background:linear-gradient(145deg,{app.tint},color-mix(in srgb,{app.tint} 60%,black))"
										>
											<Icon class="size-[18px]" />
										</span>
										<span class="min-w-0">
											<span class="block truncate text-sm font-medium text-white">{app.title}</span>
											<span class="block truncate text-[11px] text-white/45">{app.blurb}</span>
										</span>
									</button>
								</li>
							{/each}
						</ul>
					{/if}
				{/each}
				{#if filtered.length === 0}
					<p class="py-10 text-center text-sm text-white/50">No app matches “{q}”.</p>
				{/if}
			</div>
		</div>

		<aside
			class="hidden w-60 shrink-0 flex-col gap-4 border-l border-white/8 bg-black/20 p-4 sm:flex"
		>
			<div class="flex items-center gap-3">
				<span
					class="flex size-11 items-center justify-center rounded-2xl bg-gradient-to-br from-[#0f766e] to-[#134e4a] font-display text-base font-bold text-white ring-1 ring-white/15"
					>{initials}</span
				>
				<div class="min-w-0">
					<p class="truncate text-sm font-semibold text-white">{auth.me?.full_name}</p>
					<p class="truncate font-mono text-[11px] text-white/45">@{auth.me?.username}</p>
				</div>
			</div>
			<div>
				<p
					class="mb-2 font-display text-[11px] font-semibold tracking-[0.2em] text-white/45 uppercase"
				>
					Quota{#if usage}
						· {usage.quota.name}{/if}
				</p>
				{#if meters.length}
					<ul class="space-y-2.5">
						{#each meters as m (m.label)}
							{@const pct = m.max > 0 ? Math.min(100, (m.v / m.max) * 100) : 0}
							<li>
								<div class="mb-1 flex justify-between text-[11px]">
									<span class="text-white/60">{m.label}</span><span class="font-mono text-white/80"
										>{m.text}</span
									>
								</div>
								<div
									class="h-1.5 overflow-hidden rounded-full bg-white/8"
									role="meter"
									aria-label="{m.label} quota"
									aria-valuemin={0}
									aria-valuemax={100}
									aria-valuenow={Math.round(pct)}
								>
									<div
										class="h-full rounded-full {pct >= 90
											? 'bg-red-500'
											: pct >= 75
												? 'bg-amber-400'
												: 'bg-signal'}"
										style="width:{pct}%"
									></div>
								</div>
							</li>
						{/each}
					</ul>
				{:else}
					<p class="text-xs text-white/40">Quota unavailable.</p>
				{/if}
			</div>
			<div>
				<p
					class="mb-2 font-display text-[11px] font-semibold tracking-[0.2em] text-white/45 uppercase"
				>
					Appearance
				</p>
				<div class="flex gap-1 rounded-xl bg-white/6 p-1" role="group" aria-label="Window theme">
					{#each [{ v: 'light', i: Sun }, { v: 'dark', i: Moon }, { v: 'system', i: Monitor }] as t (t.v)}
						{@const I = t.i}
						<button
							class="flex flex-1 items-center justify-center rounded-lg py-1.5 transition {theme.value ===
							t.v
								? 'bg-white/15 text-white'
								: 'text-white/50 hover:text-white'}"
							onclick={() => theme.set(t.v as 'light' | 'dark' | 'system')}
							aria-pressed={theme.value === t.v}
							aria-label="{t.v} theme"
						>
							<I class="size-4" />
						</button>
					{/each}
				</div>
				<div class="mt-2 grid grid-cols-4 gap-1.5" role="group" aria-label="Wallpaper">
					{#each wallpapers as w (w.id)}
						<button
							class="aspect-[4/3] rounded-lg ring-2 transition {wm.wallpaper === w.id
								? 'ring-signal'
								: 'ring-white/10 hover:ring-white/35'}"
							style="background:{w.swatch}"
							onclick={() => wm.setWallpaper(w.id)}
							aria-label="Wallpaper {w.name}"
							aria-pressed={wm.wallpaper === w.id}
							title={w.name}
						></button>
					{/each}
				</div>
			</div>
			<div class="mt-auto space-y-1">
				<a
					href="/dashboard"
					class="flex items-center gap-2 rounded-lg px-2 py-2 text-sm text-white/70 transition hover:bg-white/8 hover:text-white"
				>
					<LayoutPanelLeft class="size-4" /> Classic console
				</a>
				<button
					class="flex w-full items-center gap-2 rounded-lg px-2 py-2 text-sm text-red-300 transition hover:bg-red-500/15"
					onclick={() => auth.logout()}
				>
					<LogOut class="size-4" /> Sign out
				</button>
			</div>
		</aside>
	</div>
{/if}
