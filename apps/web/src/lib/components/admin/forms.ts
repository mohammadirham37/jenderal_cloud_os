// Form helpers for administration pages.
import { ApiError, errorMessage } from '#lib/api/client.ts';
import { toast } from '#lib/stores/toast.svelte.ts';

/**
 * Reports a failed submit: returns the 422 field errors (to show under the
 * matching fields) and toasts the general message otherwise.
 */
export function handleFormError(e: unknown, title: string): Record<string, string> {
	if (e instanceof ApiError && e.status === 422 && Object.keys(e.details).length > 0) {
		toast.error(title, 'Check the highlighted fields.');
		return { ...e.details };
	}
	toast.error(title, errorMessage(e));
	return {};
}

/** Parses a numeric input value; returns null for empty/invalid input. */
export function num(v: unknown): number | null {
	if (v === '' || v === null || v === undefined) return null;
	const n = Number(v);
	return Number.isFinite(n) ? n : null;
}
