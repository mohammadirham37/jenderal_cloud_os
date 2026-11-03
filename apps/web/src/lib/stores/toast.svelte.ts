export type ToastKind = 'success' | 'error' | 'info' | 'warning';

export interface Toast {
	id: number;
	kind: ToastKind;
	title: string;
	message?: string;
}

class ToastState {
	items = $state<Toast[]>([]);
	#next = 1;

	push(kind: ToastKind, title: string, message?: string, timeout = kind === 'error' ? 8000 : 4000) {
		const id = this.#next++;
		this.items.push({ id, kind, title, message });
		if (timeout > 0) setTimeout(() => this.dismiss(id), timeout);
		return id;
	}

	success(title: string, message?: string) {
		return this.push('success', title, message);
	}
	error(title: string, message?: string) {
		return this.push('error', title, message);
	}
	info(title: string, message?: string) {
		return this.push('info', title, message);
	}
	warning(title: string, message?: string) {
		return this.push('warning', title, message);
	}

	dismiss(id: number) {
		this.items = this.items.filter((t) => t.id !== id);
	}
}

export const toast = new ToastState();
