<script lang="ts">
	// Minimal accessible SVG line chart for metric history. Gaps (null values)
	// break the line rather than being drawn as zero.
	interface Series {
		label: string;
		values: (number | null)[];
		color: string;
	}
	let {
		times,
		series,
		height = 180,
		format = (v: number) => v.toFixed(0),
		yMax,
		title
	}: {
		times: string[];
		series: Series[];
		height?: number;
		format?: (v: number) => string;
		yMax?: number;
		title: string;
	} = $props();

	const width = 640;
	const pad = { l: 52, r: 12, t: 10, b: 24 };
	const all = $derived(series.flatMap((s) => s.values.filter((v): v is number => v !== null)));
	const max = $derived(yMax ?? Math.max(1, ...all) * 1.1);
	const n = $derived(times.length);
	const x = (i: number) => pad.l + (n <= 1 ? 0 : (i / (n - 1)) * (width - pad.l - pad.r));
	const y = (v: number) => pad.t + (1 - v / max) * (height - pad.t - pad.b);

	function path(values: (number | null)[]): string {
		let d = '';
		let pen = false;
		values.forEach((v, i) => {
			if (v === null) {
				pen = false;
				return;
			}
			d += `${pen ? 'L' : 'M'}${x(i).toFixed(1)},${y(v).toFixed(1)} `;
			pen = true;
		});
		return d;
	}
	const ticks = $derived([0, 0.25, 0.5, 0.75, 1].map((f) => f * max));
	const timeLabels = $derived(
		n === 0
			? []
			: [0, Math.floor((n - 1) / 2), n - 1].map((i) => ({
					i,
					label: new Date(times[i]).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
				}))
	);
	let hover = $state<number | null>(null);

	function onmove(e: MouseEvent) {
		const svg = e.currentTarget as SVGSVGElement;
		const rect = svg.getBoundingClientRect();
		const px = ((e.clientX - rect.left) / rect.width) * width;
		if (n === 0) return;
		const i = Math.round(((px - pad.l) / (width - pad.l - pad.r)) * (n - 1));
		hover = Math.min(n - 1, Math.max(0, i));
	}
</script>

<figure>
	<figcaption class="sr-only">{title}</figcaption>
	{#if n === 0}
		<div class="flex items-center justify-center text-sm text-fg-muted" style="height: {height}px">
			No data collected for this period yet.
		</div>
	{:else}
		<svg
			viewBox="0 0 {width} {height}"
			class="w-full"
			role="img"
			aria-label={title}
			onmousemove={onmove}
			onmouseleave={() => (hover = null)}
		>
			{#each ticks as t (t)}
				<line
					x1={pad.l}
					x2={width - pad.r}
					y1={y(t)}
					y2={y(t)}
					class="stroke-border"
					stroke-width="1"
				/>
				<text x={pad.l - 6} y={y(t) + 4} text-anchor="end" class="fill-fg-subtle text-[10px]"
					>{format(t)}</text
				>
			{/each}
			{#each timeLabels as tl (tl.i)}
				<text x={x(tl.i)} y={height - 6} text-anchor="middle" class="fill-fg-subtle text-[10px]"
					>{tl.label}</text
				>
			{/each}
			{#each series as s (s.label)}
				<path
					d={path(s.values)}
					fill="none"
					stroke={s.color}
					stroke-width="1.75"
					stroke-linejoin="round"
				/>
			{/each}
			{#if hover !== null}
				<line
					x1={x(hover)}
					x2={x(hover)}
					y1={pad.t}
					y2={height - pad.b}
					class="stroke-border-strong"
					stroke-dasharray="3 3"
				/>
			{/if}
		</svg>
		<div class="mt-2 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-fg-muted">
			{#each series as s (s.label)}
				<span class="inline-flex items-center gap-1.5">
					<span class="h-0.5 w-3 rounded" style="background: {s.color}"></span>{s.label}
					{#if hover !== null}<strong class="text-fg tabular-nums"
							>{s.values[hover] === null ? '—' : format(s.values[hover] as number)}</strong
						>{/if}
				</span>
			{/each}
			{#if hover !== null}<span class="ml-auto tabular-nums"
					>{new Date(times[hover]).toLocaleString()}</span
				>{/if}
		</div>
	{/if}
</figure>
