// Promise-based confirmation dialog for destructive actions.

export interface ConfirmOptions {
	title: string;
	message?: string;
	confirmLabel?: string;
	cancelLabel?: string;
	danger?: boolean;
	/** When set, the user must type this text to enable the confirm button. */
	requireText?: string;
	/** When set, a reason textarea is shown and its value is returned. */
	reasonLabel?: string;
	reasonRequired?: boolean;
	/** Minimum reason length when required (default 3). */
	reasonMinLength?: number;
}

interface Pending extends ConfirmOptions {
	resolve: (v: { confirmed: boolean; reason: string }) => void;
}

class ConfirmState {
	current = $state<Pending | null>(null);

	ask(opts: ConfirmOptions): Promise<{ confirmed: boolean; reason: string }> {
		return new Promise((resolve) => {
			this.current = { ...opts, resolve };
		});
	}

	/** Convenience: resolves to true when confirmed. */
	async confirm(opts: ConfirmOptions): Promise<boolean> {
		return (await this.ask(opts)).confirmed;
	}

	close(confirmed: boolean, reason = '') {
		this.current?.resolve({ confirmed, reason });
		this.current = null;
	}
}

export const confirmDialog = new ConfirmState();
