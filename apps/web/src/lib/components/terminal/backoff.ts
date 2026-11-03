// Pure helpers for the browser terminal: reconnect backoff and the
// interpretation of WebSocket close reasons sent by the API
// (internal/modules/terminals/terminals.go).

export interface BackoffOptions {
	/** Delay before the first retry (ms). */
	baseMs?: number;
	/** Upper bound for a single delay (ms). */
	maxMs?: number;
	/** Number of retries before giving up. */
	maxAttempts?: number;
}

export const DEFAULT_BACKOFF: Required<BackoffOptions> = {
	baseMs: 1000,
	maxMs: 30000,
	maxAttempts: 8
};

/**
 * Delay before retry number `attempt` (1-based): 1s, 2s, 4s, … capped at maxMs.
 * Returns null when no further attempt should be made.
 */
export function backoffDelay(attempt: number, opts: BackoffOptions = {}): number | null {
	const o = { ...DEFAULT_BACKOFF, ...opts };
	if (!Number.isFinite(attempt) || attempt < 1 || attempt > o.maxAttempts) return null;
	const delay = o.baseMs * 2 ** (attempt - 1);
	return Math.min(delay, o.maxMs);
}

/** Close reasons after which the session is over and must not be reconnected. */
export const FINAL_REASONS = [
	'session_closed',
	'expired',
	'workspace_stopped',
	'login_session_ended',
	'shell_exited',
	'terminal_session_ended'
] as const;

export type FinalReason = (typeof FINAL_REASONS)[number];

export function isFinalReason(reason: string | null | undefined): reason is FinalReason {
	return !!reason && (FINAL_REASONS as readonly string[]).includes(reason);
}

/** Whether an unexpected close should trigger a reconnect. */
export function shouldReconnect(reason: string | null | undefined, userClosed = false): boolean {
	return !userClosed && !isFinalReason(reason);
}

/** User-facing explanation for a close reason. */
export function describeReason(reason: string | null | undefined): string {
	switch (reason) {
		case 'session_closed':
		case 'closed_by_user':
			return 'The terminal session was closed.';
		case 'expired':
			return 'The terminal session reached its maximum duration and expired.';
		case 'workspace_stopped':
			return 'The workspace was stopped, so the terminal was disconnected.';
		case 'login_session_ended':
			return 'Your login session ended. Sign in again to open a terminal.';
		case 'shell_exited':
			return 'The shell exited.';
		case 'terminal_session_ended':
			return 'This terminal session has ended.';
		case 'client_disconnected':
			return 'The connection was interrupted.';
		case '':
		case null:
		case undefined:
			return 'The connection was lost.';
		default:
			return reason;
	}
}

/** Returns a debounced version of fn plus a cancel function. */
export function debounce<A extends unknown[]>(
	fn: (...args: A) => void,
	ms: number
): { call: (...args: A) => void; cancel: () => void } {
	let t: ReturnType<typeof setTimeout> | null = null;
	return {
		call: (...args: A) => {
			if (t) clearTimeout(t);
			t = setTimeout(() => {
				t = null;
				fn(...args);
			}, ms);
		},
		cancel: () => {
			if (t) clearTimeout(t);
			t = null;
		}
	};
}
