import { goto } from '$app/navigation';
import { isEmbedded } from '#lib/desktop/embed.ts';

/** Navigates the top-level document (the desktop when running in a window). */
async function navigateTop(url: string) {
	if (isEmbedded()) {
		window.top!.location.href = url;
		return;
	}
	await goto(url);
}
import { api, setAuthRedirectHandler, setCsrfToken } from '#lib/api/client.ts';
import type { Me } from '#lib/api/types.ts';

class AuthState {
	me = $state<Me | null>(null);
	loaded = $state(false);
	#perms = $derived(new Set(this.me?.permissions ?? []));

	/** Loads the current user; returns null when not logged in. */
	async load(): Promise<Me | null> {
		try {
			const me = await api.get<Me>('/auth/me', undefined, { noAuthRedirect: true });
			this.set(me);
		} catch {
			this.set(null);
		}
		this.loaded = true;
		return this.me;
	}

	set(me: Me | null) {
		this.me = me;
		setCsrfToken(me?.csrf_token ?? '');
	}

	can(...perms: string[]): boolean {
		return perms.some((p) => this.#perms.has(p));
	}

	/** True when the session still needs MFA or a password change. */
	get restricted(): boolean {
		const m = this.me;
		if (!m) return false;
		return (m.mfa_required && !m.mfa_verified) || m.must_change_password;
	}

	/** Route the user should be sent to based on session state. */
	gate(): string | null {
		const m = this.me;
		if (!m) return '/login';
		if (m.mfa_required && !m.mfa_verified) return m.mfa_enrolled ? '/mfa' : '/mfa/setup';
		if (m.must_change_password) return '/account/password?required=1';
		return null;
	}

	async logout() {
		try {
			await api.post('/auth/logout', undefined, { noAuthRedirect: true });
		} catch {
			// session may already be gone
		}
		this.set(null);
		await navigateTop('/login');
	}
}

export const auth = new AuthState();

let redirecting = false;
setAuthRedirectHandler(async (code, status) => {
	if (redirecting) return;
	redirecting = true;
	try {
		if (status === 401) {
			const wasLoggedIn = auth.me !== null;
			auth.set(null);
			const next = encodeURIComponent(location.pathname + location.search);
			const back = isEmbedded() ? '%2F' : next;
			await navigateTop(wasLoggedIn ? `/session-expired?next=${back}` : `/login?next=${back}`);
		} else if (code === 'mfa_required' || code === 'password_change_required') {
			await auth.load();
			const target = auth.gate();
			if (target) await navigateTop(target);
		}
	} finally {
		redirecting = false;
	}
});
