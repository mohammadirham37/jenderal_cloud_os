// Wallpapers of the desktop: layered CSS backgrounds (contour lines are
// procedurally generated SVGs in /static/wallpapers; grain adds texture).

export interface Wallpaper {
	id: string;
	name: string;
	/** Light wallpapers get dark desktop-icon labels. */
	light?: boolean;
	css: string;
	swatch: string;
}

const grain = "url('/wallpapers/grain.svg')";

export const wallpapers: Wallpaper[] = [
	{
		id: 'kontur',
		name: 'Kontur',
		css: `${grain}, url('/wallpapers/contour-light.svg') center/cover no-repeat,
			radial-gradient(60% 70% at 78% 30%, rgba(255,122,26,0.22), transparent 60%),
			radial-gradient(55% 60% at 18% 85%, rgba(20,184,166,0.20), transparent 65%),
			linear-gradient(160deg, #0b1118 0%, #0e1822 55%, #0a0f15 100%)`,
		swatch: 'linear-gradient(160deg,#0e1822,#2a1a10)'
	},
	{
		id: 'fajar',
		name: 'Fajar',
		css: `${grain}, url('/wallpapers/contour-light.svg') center/cover no-repeat,
			radial-gradient(80% 60% at 70% 110%, rgba(255,166,77,0.85), transparent 60%),
			radial-gradient(70% 70% at 20% 0%, rgba(13,148,136,0.55), transparent 60%),
			linear-gradient(180deg, #102332 0%, #3b2f4a 55%, #c2552b 100%)`,
		swatch: 'linear-gradient(180deg,#102332,#c2552b)'
	},
	{
		id: 'cetak-biru',
		name: 'Cetak Biru',
		css: `${grain},
			repeating-linear-gradient(0deg, rgba(160,200,255,0.07) 0 1px, transparent 1px 32px),
			repeating-linear-gradient(90deg, rgba(160,200,255,0.07) 0 1px, transparent 1px 32px),
			repeating-linear-gradient(0deg, rgba(160,200,255,0.13) 0 1px, transparent 1px 160px),
			repeating-linear-gradient(90deg, rgba(160,200,255,0.13) 0 1px, transparent 1px 160px),
			radial-gradient(70% 60% at 50% 40%, #12407a, #0a2348 70%, #071833)`,
		swatch: 'linear-gradient(135deg,#12407a,#071833)'
	},
	{
		id: 'kertas',
		name: 'Kertas',
		light: true,
		css: `${grain}, url('/wallpapers/contour-ink.svg') center/cover no-repeat,
			radial-gradient(60% 60% at 80% 20%, rgba(255,122,26,0.18), transparent 60%),
			linear-gradient(170deg, #f4efe6 0%, #ebe4d8 100%)`,
		swatch: 'linear-gradient(170deg,#f4efe6,#d9cfbf)'
	}
];

export function wallpaperById(id: string): Wallpaper {
	return wallpapers.find((w) => w.id === id) ?? wallpapers[0];
}
