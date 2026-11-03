<!--
	Jenderal Cloud OS logo: a general's shoulder board (pangkat) with the rank
	star and a chevron cut out of it. Same geometry as /static/brand/*.svg.

	variant="mark"     the bare insignia (signal orange, transparent)
	variant="tile"     the app icon (insignia on an ink tile)
	variant="wordmark" insignia + "Jenderal / Cloud OS" (text uses currentColor)
-->
<script lang="ts">
	interface Props {
		variant?: 'mark' | 'tile' | 'wordmark';
		/** Height of the mark/tile in px. */
		size?: number;
		/** Accessible name; pass `null` when the logo is decorative. */
		label?: string | null;
		class?: string;
	}
	let {
		variant = 'mark',
		size = 32,
		label = 'Jenderal Cloud OS',
		class: cls = ''
	}: Props = $props();

	const uid = $props.id();
	const sig = `jc-sig-${uid}`;
	const tile = `jc-tile-${uid}`;
	const glow = `jc-glow-${uid}`;

	// 64-unit grid; the star (outer radius 8.5) is cut out of the upper plate.
	const UPPER =
		'M32 7L49 18V47.75L32 37.25L15 47.75V18Z' +
		'M32 19L34.06 24.67L40.08 24.87L35.33 28.58L37 34.38L32 31L27 34.38L28.67 28.58L23.92 24.87L29.94 24.67Z';
	const LOWER = 'M15 52.25L32 41.75L49 52.25V53a4 4 0 0 1-4 4H19a4 4 0 0 1-4-4Z';

	const isTile = $derived(variant === 'tile');
	// The bare mark is cropped to its bounds (x 15–49, y 7–57) plus a hair of air.
	const viewBox = $derived(isTile ? '0 0 64 64' : '13 5 38 54');
	const width = $derived(isTile ? size : (size * 38) / 54);
</script>

{#snippet art()}
	<svg
		{width}
		height={size}
		{viewBox}
		class="shrink-0"
		aria-hidden="true"
		focusable="false"
		xmlns="http://www.w3.org/2000/svg"
	>
		<defs>
			<linearGradient id={sig} x1="0" y1="7" x2="0" y2="57" gradientUnits="userSpaceOnUse">
				<stop offset="0" style="stop-color: color-mix(in oklab, var(--color-signal) 72%, white)" />
				<stop offset="1" style="stop-color: color-mix(in oklab, var(--color-signal) 88%, black)" />
			</linearGradient>
			{#if isTile}
				<linearGradient id={tile} x1="0" y1="0" x2="0" y2="1">
					<stop offset="0" style="stop-color: color-mix(in oklab, var(--color-ink) 90%, white)" />
					<stop offset="1" style="stop-color: var(--color-ink)" />
				</linearGradient>
				<radialGradient id={glow} cx=".5" cy=".36" r=".62">
					<stop offset="0" style="stop-color: var(--color-signal); stop-opacity: 0.3" />
					<stop offset="1" style="stop-color: var(--color-signal); stop-opacity: 0" />
				</radialGradient>
			{/if}
		</defs>
		{#if isTile}
			<rect width="64" height="64" rx="15" fill="url(#{tile})" />
			<rect width="64" height="64" rx="15" fill="url(#{glow})" />
			<rect
				x=".5"
				y=".5"
				width="63"
				height="63"
				rx="14.5"
				fill="none"
				stroke="white"
				stroke-opacity=".1"
			/>
		{/if}
		<path fill="url(#{sig})" fill-rule="evenodd" d={UPPER} />
		<path fill="url(#{sig})" d={LOWER} />
	</svg>
{/snippet}

{#if variant === 'wordmark'}
	<span
		class="inline-flex items-center gap-2.5 {cls}"
		role={label ? 'img' : undefined}
		aria-label={label ?? undefined}
		aria-hidden={label ? undefined : 'true'}
	>
		{@render art()}
		<span class="flex flex-col leading-none" aria-hidden="true">
			<span
				class="font-display font-bold tracking-[0.04em] uppercase"
				style="font-size: {size * 0.52}px">Jenderal</span
			>
			<span
				class="mt-[0.35em] font-mono font-medium tracking-[0.42em] text-signal uppercase"
				style="font-size: {Math.max(9, size * 0.2)}px">Cloud OS</span
			>
		</span>
	</span>
{:else}
	<span
		class="inline-flex shrink-0 {cls}"
		role={label ? 'img' : undefined}
		aria-label={label ?? undefined}
		aria-hidden={label ? undefined : 'true'}
	>
		{@render art()}
	</span>
{/if}
