// Client-side permission guard for administration pages. The backend
// enforces every permission anyway; this only avoids pointless requests and
// renders a friendly "Access denied" state.
import { ApiError } from '#lib/api/client.ts';
import { auth } from '#lib/stores/auth.svelte.ts';

export function forbidden(): ApiError {
	return new ApiError(403, {
		code: 'forbidden',
		message:
			'You do not have permission to view this page. Ask an administrator if you need access.'
	});
}

/** Wraps a loader so it fails with a 403 when the user lacks every permission. */
export function guarded<T>(perms: string[], fn: () => Promise<T>): () => Promise<T> {
	return () => (auth.can(...perms) ? fn() : Promise.reject(forbidden()));
}
