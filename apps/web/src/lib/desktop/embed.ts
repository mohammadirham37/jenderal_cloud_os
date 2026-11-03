// Bridge between console pages rendered inside desktop windows (iframes) and
// the desktop shell. Messages are same-origin only.
import { appForPath, parseWindowName } from './apps';

export type ShellMessage =
	| { type: 'itk:nav'; title: string; path: string }
	| { type: 'itk:open'; appId: string; url: string }
	| { type: 'itk:signout' };

/** True when this document runs inside a desktop window. */
export function isEmbedded(): boolean {
	try {
		return window.self !== window.top && parseWindowName(window.name) !== null;
	} catch {
		return true; // cross-origin parent: treat as embedded but never post to it
	}
}

export function postToShell(msg: ShellMessage) {
	if (!isEmbedded()) return;
	window.parent.postMessage(msg, location.origin);
}

/**
 * Intercepts clicks on links that belong to a different desktop app and asks
 * the shell to open them in that app's window instead. Returns a cleanup fn.
 */
export function interceptCrossAppLinks(): () => void {
	const own = parseWindowName(window.name)?.appId;
	const handler = (e: MouseEvent) => {
		if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey)
			return;
		const a = (e.target as Element | null)?.closest?.('a[href]') as HTMLAnchorElement | null;
		if (!a || a.target || a.hasAttribute('download')) return;
		const url = new URL(a.href, location.href);
		if (url.origin !== location.origin || url.pathname.startsWith('/api/')) return;
		const app = appForPath(url.pathname);
		if (!app || app.id === own) return;
		e.preventDefault();
		postToShell({ type: 'itk:open', appId: app.id, url: url.pathname + url.search });
	};
	document.addEventListener('click', handler, true);
	return () => document.removeEventListener('click', handler, true);
}
