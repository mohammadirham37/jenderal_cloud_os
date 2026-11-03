// Role rank rules mirrored from internal/modules/rbac (CanGrant). The server
// enforces them; the UI only hides choices that would be rejected.
import type { Role } from '#lib/api/types.ts';
import { auth } from '#lib/stores/auth.svelte.ts';

/** Highest rank among the current user's roles (0 when unknown). */
export function myMaxRank(roles: Role[]): number {
	const mine = new Set(auth.me?.roles ?? []);
	return roles.reduce((max, r) => (mine.has(r.key) && r.rank > max ? r.rank : max), 0);
}

/** Whether the current user may grant or revoke a role of the given rank. */
export function canGrant(rank: number, maxRank: number): boolean {
	if (auth.can('roles.manage')) return rank <= maxRank;
	return auth.can('users.assign_roles') && rank < maxRank;
}
