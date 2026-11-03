// Shared helpers for the workspaces / containers pages.
import { api, ApiError, errorMessage } from '#lib/api/client.ts';
import type { Container, Workspace } from '#lib/api/types.ts';
import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
import { toast } from '#lib/stores/toast.svelte.ts';

/** Workspace statuses that change on their own (worker jobs in flight). */
export const TRANSIENT_WS = ['requested', 'provisioning', 'deleting'];

/** Container statuses that change on their own. */
export const TRANSIENT_CTR = ['starting', 'stopping', 'removing'];

export const WORKSPACE_STATUSES = [
	'requested',
	'provisioning',
	'ready',
	'running',
	'stopped',
	'suspended',
	'failed',
	'deleting'
] as const;

export const CONTAINER_STATUSES = [
	'created',
	'starting',
	'running',
	'stopping',
	'stopped',
	'failed',
	'removing'
] as const;

/** True when the workspace is changing state and should be polled quickly. */
export function isWorkspaceBusy(w: Pick<Workspace, 'status' | 'pending_job'>): boolean {
	return TRANSIENT_WS.includes(w.status) || w.pending_job !== null;
}

/** Access levels (from the list endpoint) that allow start/stop. */
export function canOperate(access: Workspace['access']): boolean {
	return access === 'owner' || access === 'editor' || access === 'operator';
}

/** Statuses in which the workspace can be started (mirrors the backend state machine). */
export function canStart(status: string): boolean {
	return status === 'ready' || status === 'stopped' || status === 'suspended';
}

export function canStop(status: string): boolean {
	return status === 'running';
}

/** Statuses from which a workspace can be deleted. */
export function canDelete(status: string): boolean {
	return ['ready', 'stopped', 'failed', 'suspended'].includes(status);
}

/** Toasts an API error, using a clearer title for conflicts and quota errors. */
export function toastError(title: string, e: unknown) {
	if (e instanceof ApiError) {
		if (e.code === 'quota_exceeded') return toast.error('Quota exceeded', e.message);
		if (e.status === 409) return toast.warning(title, e.message);
		if (e.status === 403) return toast.error('Not allowed', e.message);
	}
	return toast.error(title, errorMessage(e));
}

/**
 * Runs `fn`; when the server answers that emergency access needs a
 * justification (administrators acting on another user's workspace data),
 * asks for one and retries with the X-Access-Reason header. Resolves to
 * `undefined` when the user cancels.
 */
export async function withAccessReason<T>(
	fn: (headers: Record<string, string>) => Promise<T>
): Promise<T | undefined> {
	try {
		return await fn({});
	} catch (e) {
		if (!(e instanceof ApiError) || e.code !== 'emergency_reason_required') throw e;
		const { confirmed, reason } = await confirmDialog.ask({
			title: 'Emergency access',
			message:
				'This workspace belongs to another user. Accessing its data is audited and requires a justification (at least 10 characters).',
			reasonLabel: 'Justification',
			reasonRequired: true,
			confirmLabel: 'Continue',
			danger: true
		});
		if (!confirmed) return undefined;
		if (reason.length < 10) {
			toast.error('Justification too short', 'Enter at least 10 characters.');
			return undefined;
		}
		return await fn({ 'X-Access-Reason': reason });
	}
}

/**
 * Starts / stops / restarts a container after confirmation (stop/restart).
 * Returns the updated container, or undefined when cancelled or failed.
 */
export async function containerAction(
	c: Pick<Container, 'id' | 'name' | 'is_primary'>,
	action: 'start' | 'stop' | 'restart'
): Promise<Container | undefined> {
	if (action !== 'start') {
		const ok = await confirmDialog.confirm({
			title: `${action === 'stop' ? 'Stop' : 'Restart'} container ${c.name}?`,
			message:
				action === 'stop'
					? `Processes in this container will be stopped${c.is_primary ? ' and terminals into the workspace will disconnect' : ''}. Files in the workspace volume are kept.`
					: 'Processes in this container will be restarted and attached terminals disconnected.',
			confirmLabel: action === 'stop' ? 'Stop container' : 'Restart container',
			danger: true
		});
		if (!ok) return undefined;
	}
	try {
		const out = await api.post<Container>(`/containers/${c.id}/${action}`);
		toast.success(
			{ start: 'Starting container', stop: 'Stopping container', restart: 'Restarting container' }[
				action
			],
			c.name
		);
		return out;
	} catch (e) {
		toastError(`Could not ${action} ${c.name}`, e);
		return undefined;
	}
}

/** Converts a number of whole/fractional units to an integer (e.g. GiB -> bytes). */
export function toUnits(value: number, unit: number): number {
	return Math.round(value * unit);
}

/** Converts an integer to a rounded unit value (e.g. bytes -> GiB with 2 decimals). */
export function fromUnits(value: number, unit: number, digits = 2): number {
	const f = 10 ** digits;
	return Math.round((value / unit) * f) / f;
}

/** Idle timeout label. */
export function idleLabel(min: number | null | undefined): string {
	if (min === null || min === undefined || min === 0) return 'Disabled';
	if (min % 60 === 0) return `${min / 60} h`;
	return `${min} min`;
}
