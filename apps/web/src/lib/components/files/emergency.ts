// Emergency access (workspaces.access_all) to another user's workspace data
// requires a justification sent in the X-Access-Reason header (min. 10 chars,
// see internal/modules/workspaces/access.go). Used by the file manager and
// the terminal page.
import { ApiError } from '#lib/api/client.ts';
import { confirmDialog } from '#lib/stores/confirm.svelte.ts';
import { toast } from '#lib/stores/toast.svelte.ts';

export const ACCESS_REASON_HEADER = 'X-Access-Reason';
export const MIN_REASON_LENGTH = 10;

export function isEmergencyRequired(e: unknown): boolean {
	return e instanceof ApiError && e.status === 403 && e.code === 'emergency_reason_required';
}

/** Asks for a justification; resolves to null when the user cancels. */
export async function askEmergencyReason(action: string): Promise<string | null> {
	for (;;) {
		const r = await confirmDialog.ask({
			title: 'Emergency access',
			message: `This workspace belongs to another user. To ${action}, give a justification (at least ${MIN_REASON_LENGTH} characters). It is recorded in the audit log together with every action you take.`,
			reasonLabel: 'Reason for access',
			reasonRequired: true,
			confirmLabel: 'Continue'
		});
		if (!r.confirmed) return null;
		const reason = r.reason.trim();
		if (reason.length >= MIN_REASON_LENGTH) return reason;
		toast.error(
			'Reason too short',
			`Please describe why you need access in at least ${MIN_REASON_LENGTH} characters.`
		);
	}
}
