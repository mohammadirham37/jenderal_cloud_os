import { api } from '#lib/api/client.ts';

/** Polls the unread notification count while the app is open. */
class NotificationCounter {
	unread = $state(0);
	#timer: ReturnType<typeof setInterval> | null = null;

	async refresh() {
		try {
			const r = await api.get<{ unread: number }>('/notifications/unread-count');
			this.unread = r.unread;
		} catch {
			// keep last value
		}
	}

	start() {
		this.refresh();
		this.stop();
		this.#timer = setInterval(() => {
			if (document.visibilityState === 'visible') this.refresh();
		}, 30000);
	}

	stop() {
		if (this.#timer) clearInterval(this.#timer);
		this.#timer = null;
	}
}

export const notificationCounter = new NotificationCounter();
