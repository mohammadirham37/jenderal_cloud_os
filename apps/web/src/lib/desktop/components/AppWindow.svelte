<script lang="ts">
	import { Copy, Minus, Square, X } from '@lucide/svelte';
	import { appById } from '../apps';
	import { wm, type Snap, type Win } from '../windows.svelte';

	let { win }: { win: Win } = $props();

	const app = $derived(appById(win.appId)!);
	const Icon = $derived(app.icon);
	const rect = $derived(wm.rect(win));
	const focused = $derived(wm.focusedId === win.id);
	const maximized = $derived(win.snap === 'max' || wm.compact);
	let frame = $state<HTMLIFrameElement>();
	let loading = $state(true);

	// ----- dragging ----------------------------------------------------------
	function startDrag(e: PointerEvent) {
		if (e.button !== 0 || wm.compact || (e.target as Element).closest('button')) return;
		e.preventDefault();
		wm.focus(win.id);
		const start = { px: e.clientX, py: e.clientY };
		let origin = { x: rect.x, y: rect.y };
		let restored = win.snap === 'none';
		const el = e.currentTarget as HTMLElement;
		el.setPointerCapture(e.pointerId);
		wm.interacting = true;

		const move = (ev: PointerEvent) => {
			const dx = ev.clientX - start.px;
			const dy = ev.clientY - start.py;
			if (!restored) {
				if (Math.abs(dx) + Math.abs(dy) < 6) return;
				// Pull a snapped window out: restore its size under the cursor.
				const ratio = (start.px - rect.x) / rect.w;
				wm.setSnap(win.id, 'none');
				origin = { x: ev.clientX - win.w * ratio, y: ev.clientY - 18 };
				start.px = ev.clientX;
				start.py = ev.clientY;
				restored = true;
				return;
			}
			wm.moveTo(win.id, origin.x + dx, origin.y + dy);
			const edge = 6;
			wm.snapPreview =
				ev.clientY <= edge
					? 'max'
					: ev.clientX <= edge
						? 'left'
						: ev.clientX >= wm.viewport.w - edge
							? 'right'
							: 'none';
		};
		const up = () => {
			el.removeEventListener('pointermove', move);
			el.removeEventListener('pointerup', up);
			el.removeEventListener('pointercancel', up);
			if (wm.snapPreview !== 'none') wm.setSnap(win.id, wm.snapPreview);
			wm.snapPreview = 'none';
			wm.interacting = false;
			wm.save();
		};
		el.addEventListener('pointermove', move);
		el.addEventListener('pointerup', up);
		el.addEventListener('pointercancel', up);
	}

	// ----- resizing ----------------------------------------------------------
	type Edge = 'n' | 's' | 'e' | 'w' | 'ne' | 'nw' | 'se' | 'sw';
	const edges: { edge: Edge; cls: string; cursor: string }[] = [
		{ edge: 'n', cls: 'top-0 left-3 right-3 h-1.5 -translate-y-1', cursor: 'ns-resize' },
		{ edge: 's', cls: 'bottom-0 left-3 right-3 h-1.5 translate-y-1', cursor: 'ns-resize' },
		{ edge: 'w', cls: 'left-0 top-3 bottom-3 w-1.5 -translate-x-1', cursor: 'ew-resize' },
		{ edge: 'e', cls: 'right-0 top-3 bottom-3 w-1.5 translate-x-1', cursor: 'ew-resize' },
		{ edge: 'nw', cls: 'top-0 left-0 size-3.5 -translate-1', cursor: 'nwse-resize' },
		{ edge: 'se', cls: 'bottom-0 right-0 size-3.5 translate-1', cursor: 'nwse-resize' },
		{
			edge: 'ne',
			cls: 'top-0 right-0 size-3.5 translate-x-1 -translate-y-1',
			cursor: 'nesw-resize'
		},
		{
			edge: 'sw',
			cls: 'bottom-0 left-0 size-3.5 -translate-x-1 translate-y-1',
			cursor: 'nesw-resize'
		}
	];

	function startResize(e: PointerEvent, edge: Edge) {
		if (e.button !== 0) return;
		e.preventDefault();
		e.stopPropagation();
		wm.focus(win.id);
		const r0 = { ...rect };
		if (win.snap !== 'none') wm.setSnap(win.id, 'none');
		const sx = e.clientX;
		const sy = e.clientY;
		const el = e.currentTarget as HTMLElement;
		el.setPointerCapture(e.pointerId);
		wm.interacting = true;
		const move = (ev: PointerEvent) => {
			const dx = ev.clientX - sx;
			const dy = ev.clientY - sy;
			const r = { ...r0 };
			if (edge.includes('e')) r.w = r0.w + dx;
			if (edge.includes('s')) r.h = r0.h + dy;
			if (edge.includes('w')) {
				r.w = Math.max(420, r0.w - dx);
				r.x = r0.x + r0.w - r.w;
			}
			if (edge.includes('n')) {
				r.h = Math.max(300, r0.h - dy);
				r.y = r0.y + r0.h - r.h;
			}
			wm.resizeTo(win.id, r);
		};
		const up = () => {
			el.removeEventListener('pointermove', move);
			el.removeEventListener('pointerup', up);
			wm.interacting = false;
			wm.save();
		};
		el.addEventListener('pointermove', move);
		el.addEventListener('pointerup', up);
	}

	function onTitleKey(e: KeyboardEvent) {
		// Keyboard snapping for accessibility: Alt + arrows.
		if (!e.altKey) return;
		const map: Record<string, Snap> = {
			ArrowLeft: 'left',
			ArrowRight: 'right',
			ArrowUp: 'max',
			ArrowDown: 'none'
		};
		if (map[e.key]) {
			e.preventDefault();
			wm.setSnap(win.id, map[e.key]);
		}
	}

	export function contentWindow(): Window | null {
		return frame?.contentWindow ?? null;
	}

	const shortPath = $derived(win.path.length > 48 ? win.path.slice(0, 46) + '…' : win.path);
</script>

<section
	class="absolute flex flex-col overflow-hidden rounded-xl bg-panel text-fg shadow-[0_30px_80px_-20px_rgba(0,0,0,0.7)] ring-1 transition-[opacity,transform,box-shadow] duration-200
		{focused ? 'ring-white/15' : 'ring-white/8 saturate-[0.92]'}
		{win.minimized ? 'pointer-events-none translate-y-6 scale-95 opacity-0' : 'animate-window-in'}
		{maximized ? 'rounded-none sm:rounded-lg' : ''}"
	style="left:{rect.x}px; top:{rect.y}px; width:{rect.w}px; height:{rect.h}px; z-index:{win.z};"
	aria-label="{win.title} window"
	aria-hidden={win.minimized}
	data-window={win.appId}
	onpointerdown={() => wm.focus(win.id)}
>
	<!-- Focus indicator: signal lamp line -->
	<div class="h-0.5 w-full transition-colors {focused ? 'bg-signal' : 'bg-transparent'}"></div>
	<header
		class="flex h-10 shrink-0 cursor-default items-center gap-2.5 bg-[#111821] pr-1 pl-3 text-[#dfe6ee] select-none"
		onpointerdown={startDrag}
		ondblclick={(e) => !(e.target as Element).closest('button') && wm.toggleMaximize(win.id)}
		onkeydown={onTitleKey}
		tabindex="0"
		role="toolbar"
		aria-label="{win.title} title bar (Alt+arrow keys to snap)"
	>
		<span
			class="flex size-5 shrink-0 items-center justify-center rounded-md text-white"
			style="background:{app.tint}"
		>
			<Icon class="size-3" aria-hidden="true" />
		</span>
		<h2 class="truncate font-display text-[13px] font-semibold tracking-wide">{win.title}</h2>
		<span class="hidden truncate font-mono text-[11px] text-white/35 md:inline">{shortPath}</span>
		<div class="ml-auto flex items-center">
			<button
				class="flex size-8 items-center justify-center rounded-md text-white/70 hover:bg-white/10 hover:text-white"
				onclick={() => wm.minimize(win.id)}
				aria-label="Minimize {win.title}"
			>
				<Minus class="size-4" />
			</button>
			{#if !wm.compact}
				<button
					class="flex size-8 items-center justify-center rounded-md text-white/70 hover:bg-white/10 hover:text-white"
					onclick={() => wm.toggleMaximize(win.id)}
					aria-label={win.snap === 'max' ? 'Restore' : 'Maximize'}
				>
					{#if win.snap === 'max'}<Copy class="size-3.5" />{:else}<Square class="size-3.5" />{/if}
				</button>
			{/if}
			<button
				class="flex size-8 items-center justify-center rounded-md text-white/70 hover:bg-[#e5484d] hover:text-white"
				onclick={() => wm.close(win.id)}
				aria-label="Close {win.title}"
			>
				<X class="size-4" />
			</button>
		</div>
	</header>
	<div class="relative min-h-0 flex-1 bg-surface">
		{#if loading}
			<div class="absolute inset-0 flex items-center justify-center">
				<div class="flex flex-col items-center gap-3 text-fg-muted">
					<span
						class="flex size-12 items-center justify-center rounded-2xl text-white shadow-lg"
						style="background:{app.tint}"
					>
						<Icon class="size-6" />
					</span>
					<span class="h-1 w-24 overflow-hidden rounded-full bg-panel-2"
						><span
							class="block h-full w-1/3 animate-[loadbar_1s_ease-in-out_infinite] rounded-full bg-signal"
						></span></span
					>
				</div>
			</div>
		{/if}
		<iframe
			bind:this={frame}
			src={win.src}
			name="itk-win:{win.appId}:{win.id}"
			title={win.title}
			class="absolute inset-0 size-full border-0 transition-opacity duration-200 {loading
				? 'opacity-0'
				: 'opacity-100'} {wm.interacting ? 'pointer-events-none' : ''}"
			onload={() => (loading = false)}
		></iframe>
	</div>
	{#if !maximized}
		{#each edges as e (e.edge)}
			<div
				class="absolute z-10 {e.cls}"
				style="cursor:{e.cursor}"
				onpointerdown={(ev) => startResize(ev, e.edge)}
				aria-hidden="true"
			></div>
		{/each}
	{/if}
</section>

<style>
	@keyframes loadbar {
		0% {
			transform: translateX(-100%);
		}
		100% {
			transform: translateX(300%);
		}
	}
</style>
