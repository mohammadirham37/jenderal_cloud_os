// Window manager for the desktop shell: open/focus/close, minimise,
// maximise, snapping, cascade placement and per-user persistence.
import { appById, type DesktopApp } from './apps';
import { DEFAULT_WALLPAPER } from './wallpapers';

export type Snap = 'none' | 'left' | 'right' | 'max';

export interface Win {
	id: string;
	appId: string;
	title: string;
	/** URL the iframe was created with (changing it reloads the window). */
	src: string;
	/** Current in-window path, reported by the embedded app. */
	path: string;
	x: number;
	y: number;
	w: number;
	h: number;
	z: number;
	minimized: boolean;
	snap: Snap;
}

interface Saved {
	windows: Omit<Win, 'src' | 'z'>[];
	focused: string | null;
	wallpaper: string;
}

const TASKBAR = 64; // reserved height at the bottom (taskbar + gap)
const MIN_W = 420;
const MIN_H = 300;

function rid(): string {
	return Math.random().toString(36).slice(2, 8);
}

export interface Viewport {
	w: number;
	h: number;
}

export class WindowManager {
	windows = $state<Win[]>([]);
	focusedId = $state<string | null>(null);
	/** True while a window is dragged/resized (iframes stop receiving events). */
	interacting = $state(false);
	/** Snap target preview during a drag. */
	snapPreview = $state<Snap>('none');
	wallpaper = $state(DEFAULT_WALLPAPER);
	viewport = $state<Viewport>({ w: 1280, h: 800 });
	#z = 10;
	#key = '';

	get compact(): boolean {
		return this.viewport.w < 768;
	}

	get focused(): Win | undefined {
		return this.windows.find((w) => w.id === this.focusedId);
	}

	/** Effective geometry, taking snapping and compact screens into account. */
	rect(win: Win): { x: number; y: number; w: number; h: number } {
		const vw = this.viewport.w;
		const vh = this.viewport.h - TASKBAR;
		if (this.compact || win.snap === 'max') return { x: 0, y: 0, w: vw, h: vh };
		if (win.snap === 'left') return { x: 0, y: 0, w: Math.floor(vw / 2), h: vh };
		if (win.snap === 'right') return { x: Math.ceil(vw / 2), y: 0, w: Math.floor(vw / 2), h: vh };
		return { x: win.x, y: win.y, w: win.w, h: win.h };
	}

	open(appId: string, url?: string): Win | undefined {
		const app = appById(appId);
		if (!app) return undefined;
		if (app.singleton) {
			const existing = this.windows.find((w) => w.appId === appId);
			if (existing) {
				if (url && url !== existing.path) {
					existing.src = url;
					existing.path = url;
				}
				this.focus(existing.id);
				return existing;
			}
		}
		const win = this.#create(app, url ?? app.url);
		this.windows.push(win);
		this.focus(win.id);
		this.save();
		return this.windows[this.windows.length - 1];
	}

	#create(app: DesktopApp, url: string): Win {
		const size = app.size ?? { w: 1000, h: 640 };
		const vw = this.viewport.w;
		const vh = this.viewport.h - TASKBAR;
		const w = Math.min(size.w, vw - 40);
		const h = Math.min(size.h, vh - 40);
		const n = this.windows.filter((x) => !x.minimized).length;
		const x = Math.max(12, Math.round((vw - w) / 2) + ((n * 28) % 160) - 60);
		const y = Math.max(12, Math.round((vh - h) / 2) + ((n * 28) % 160) - 60);
		return {
			id: rid(),
			appId: app.id,
			title: app.title,
			src: url,
			path: url,
			x,
			y,
			w,
			h,
			z: ++this.#z,
			minimized: false,
			snap: 'none'
		};
	}

	focus(id: string) {
		const win = this.windows.find((w) => w.id === id);
		if (!win) return;
		win.minimized = false;
		if (win.z !== this.#z) win.z = ++this.#z;
		this.focusedId = id;
		this.save();
	}

	close(id: string) {
		this.windows = this.windows.filter((w) => w.id !== id);
		if (this.focusedId === id) this.#focusTop();
		this.save();
	}

	minimize(id: string) {
		const win = this.windows.find((w) => w.id === id);
		if (!win) return;
		win.minimized = true;
		if (this.focusedId === id) this.#focusTop();
		this.save();
	}

	/** Taskbar click: focus, or minimise if it is already the focused window. */
	toggleFromTaskbar(id: string) {
		const win = this.windows.find((w) => w.id === id);
		if (!win) return;
		if (this.focusedId === id && !win.minimized) this.minimize(id);
		else this.focus(id);
	}

	toggleMaximize(id: string) {
		const win = this.windows.find((w) => w.id === id);
		if (!win) return;
		win.snap = win.snap === 'max' ? 'none' : 'max';
		this.save();
	}

	setSnap(id: string, snap: Snap) {
		const win = this.windows.find((w) => w.id === id);
		if (win) {
			win.snap = snap;
			this.save();
		}
	}

	showDesktop() {
		for (const w of this.windows) w.minimized = true;
		this.focusedId = null;
		this.save();
	}

	moveTo(id: string, x: number, y: number) {
		const win = this.windows.find((w) => w.id === id);
		if (!win) return;
		const vw = this.viewport.w;
		const vh = this.viewport.h - TASKBAR;
		// Keep at least part of the title bar reachable.
		win.x = Math.round(Math.min(Math.max(x, 80 - win.w), vw - 80));
		win.y = Math.round(Math.min(Math.max(y, 0), vh - 40));
	}

	resizeTo(id: string, r: { x: number; y: number; w: number; h: number }) {
		const win = this.windows.find((w) => w.id === id);
		if (!win) return;
		win.w = Math.max(MIN_W, Math.round(r.w));
		win.h = Math.max(MIN_H, Math.round(r.h));
		win.x = Math.round(r.x);
		win.y = Math.max(0, Math.round(r.y));
	}

	report(id: string, title: string, path: string) {
		const win = this.windows.find((w) => w.id === id);
		if (!win) return;
		if (title) win.title = title;
		win.path = path;
		this.save();
	}

	#focusTop() {
		const visible = this.windows.filter((w) => !w.minimized).sort((a, b) => b.z - a.z);
		this.focusedId = visible[0]?.id ?? null;
	}

	// ----- persistence (per user) -------------------------------------------

	load(userId: string) {
		this.#key = `itk-desktop:${userId}`;
		try {
			const raw = localStorage.getItem(this.#key);
			if (!raw) return;
			const s = JSON.parse(raw) as Saved;
			this.wallpaper = s.wallpaper || DEFAULT_WALLPAPER;
			this.windows = (s.windows ?? [])
				.filter((w) => appById(w.appId))
				.map((w) => ({ ...w, src: w.path, z: ++this.#z }));
			this.focusedId = s.focused && this.windows.some((w) => w.id === s.focused) ? s.focused : null;
			const f = this.focused;
			if (f) f.z = ++this.#z;
		} catch {
			// corrupt state: start clean
		}
	}

	save() {
		if (!this.#key) return;
		const s: Saved = {
			windows: this.windows.map((w) => ({
				id: w.id,
				appId: w.appId,
				title: w.title,
				path: w.path,
				x: w.x,
				y: w.y,
				w: w.w,
				h: w.h,
				minimized: w.minimized,
				snap: w.snap
			})),
			focused: this.focusedId,
			wallpaper: this.wallpaper
		};
		try {
			localStorage.setItem(this.#key, JSON.stringify(s));
		} catch {
			// storage full or disabled: the desktop still works
		}
	}

	setWallpaper(id: string) {
		this.wallpaper = id;
		this.save();
	}
}

export const wm = new WindowManager();
