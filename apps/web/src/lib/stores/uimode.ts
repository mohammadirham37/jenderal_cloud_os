// Remembers whether the user prefers the desktop shell ("OS mode") or the
// classic console. Stored per browser; switching is always one click away
// (Start menu → Classic console, Classic top bar → OS mode).
export type UiMode = 'desktop' | 'classic';

const KEY = 'jc-ui-mode';

export function getUiMode(): UiMode {
	try {
		return localStorage.getItem(KEY) === 'classic' ? 'classic' : 'desktop';
	} catch {
		return 'desktop';
	}
}

export function setUiMode(mode: UiMode) {
	try {
		localStorage.setItem(KEY, mode);
	} catch {
		// storage unavailable: the choice just isn't remembered
	}
}
