// Small helper for loading data with loading / error state and optional
// polling (used by pages that show live status).

export interface Resource<T> {
	readonly data: T | undefined;
	readonly loading: boolean;
	readonly error: unknown;
	reload(): Promise<void>;
	/** Updates data locally (e.g. after a mutation response). */
	set(v: T): void;
}

/**
 * Loads data and keeps loading/error state. The fetch function is tracked
 * reactively: reactive values read before its first `await` (filters, page,
 * search) trigger an automatic re-fetch when they change. `poll` may be a
 * number of milliseconds or a function (re-evaluated reactively) so pages can
 * poll quickly only while something is pending; return 0 to stop polling.
 */
export function resource<T>(
	fn: () => Promise<T>,
	opts: { poll?: number | (() => number); immediate?: boolean } = {}
): Resource<T> {
	let data = $state<T | undefined>(undefined);
	let loading = $state(true);
	let error = $state<unknown>(null);
	let seq = 0;

	async function reload() {
		const my = ++seq;
		loading = true;
		try {
			const v = await fn();
			if (my === seq) {
				data = v;
				error = null;
			}
		} catch (e) {
			if (my === seq) error = e;
		} finally {
			if (my === seq) loading = false;
		}
	}

	$effect(() => {
		if (opts.immediate !== false) reload();
	});

	$effect(() => {
		const poll = opts.poll;
		const interval = typeof poll === 'function' ? poll() : (poll ?? 0);
		if (!interval) return;
		const t = setInterval(() => {
			if (document.visibilityState === 'visible') reload();
		}, interval);
		return () => clearInterval(t);
	});

	return {
		get data() {
			return data;
		},
		get loading() {
			return loading;
		},
		get error() {
			return error;
		},
		reload,
		set(v: T) {
			data = v;
		}
	};
}
