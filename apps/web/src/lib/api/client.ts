// Typed client for the Jenderal Cloud OS REST API (/api/v1).
// - Sends the per-session CSRF token on state-changing requests.
// - Normalises errors into ApiError (never exposes raw server internals).
// - Redirects to login / MFA / password change when the server requires it.

export interface ApiErrorBody {
	code: string;
	message: string;
	details?: Record<string, string>;
	request_id?: string;
}

export class ApiError extends Error {
	status: number;
	code: string;
	details: Record<string, string>;
	requestId?: string;

	constructor(status: number, body: Partial<ApiErrorBody>) {
		super(body.message || `Request failed (HTTP ${status})`);
		this.status = status;
		this.code = body.code || 'error';
		this.details = body.details || {};
		this.requestId = body.request_id;
	}

	/** Field errors for forms (422). */
	get fieldErrors(): Record<string, string> {
		return this.details;
	}
}

export interface ListMeta {
	page: number;
	per_page: number;
	total: number;
	[key: string]: unknown;
}

export interface ListResponse<T> {
	data: T[];
	meta: ListMeta;
}

let csrfToken = '';

export function setCsrfToken(token: string) {
	csrfToken = token;
}

type AuthRedirectHandler = (code: string, status: number) => void;
let onAuthRedirect: AuthRedirectHandler | null = null;

/** Registers the handler invoked for 401 / mfa_required / password_change_required. */
export function setAuthRedirectHandler(fn: AuthRedirectHandler) {
	onAuthRedirect = fn;
}

export interface RequestOptions {
	query?: Record<string, string | number | boolean | undefined | null>;
	body?: unknown;
	headers?: Record<string, string>;
	signal?: AbortSignal;
	/** Do not trigger global auth redirects (used by the auth pages). */
	noAuthRedirect?: boolean;
}

export function buildUrl(path: string, query?: RequestOptions['query']): string {
	const url = path.startsWith('/api/') ? path : `/api/v1${path}`;
	if (!query) return url;
	const params = new URLSearchParams();
	for (const [k, v] of Object.entries(query)) {
		if (v !== undefined && v !== null && v !== '') params.set(k, String(v));
	}
	const qs = params.toString();
	return qs ? `${url}?${qs}` : url;
}

async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
	const headers: Record<string, string> = { Accept: 'application/json', ...opts.headers };
	let body: BodyInit | undefined;
	if (opts.body instanceof FormData) {
		body = opts.body;
	} else if (opts.body !== undefined) {
		headers['Content-Type'] = 'application/json';
		body = JSON.stringify(opts.body);
	}
	if (method !== 'GET' && method !== 'HEAD' && csrfToken) {
		headers['X-CSRF-Token'] = csrfToken;
	}
	let res: Response;
	try {
		res = await fetch(buildUrl(path, opts.query), {
			method,
			headers,
			body,
			credentials: 'same-origin',
			signal: opts.signal
		});
	} catch (e) {
		if ((e as Error).name === 'AbortError') throw e;
		throw new ApiError(0, {
			code: 'network_error',
			message: 'Cannot reach the Jenderal Cloud OS server. Check your connection.'
		});
	}
	if (res.status === 204) return undefined as T;
	let json: unknown = null;
	const text = await res.text();
	if (text) {
		try {
			json = JSON.parse(text);
		} catch {
			json = null;
		}
	}
	if (!res.ok) {
		const errBody = ((json as { error?: ApiErrorBody })?.error ?? {}) as Partial<ApiErrorBody>;
		const err = new ApiError(res.status, errBody);
		if (!opts.noAuthRedirect && onAuthRedirect) {
			if (
				res.status === 401 ||
				err.code === 'mfa_required' ||
				err.code === 'password_change_required'
			) {
				onAuthRedirect(err.code, res.status);
			}
		}
		throw err;
	}
	return json as T;
}

/** Returns `data` from a {"data": ...} envelope. */
async function data<T>(method: string, path: string, opts?: RequestOptions): Promise<T> {
	const res = await request<{ data: T } | undefined>(method, path, opts);
	return (res as { data: T } | undefined)?.data as T;
}

export const api = {
	get: <T>(path: string, query?: RequestOptions['query'], opts?: RequestOptions) =>
		data<T>('GET', path, { ...opts, query }),
	list: <T>(path: string, query?: RequestOptions['query'], opts?: RequestOptions) =>
		request<ListResponse<T>>('GET', path, { ...opts, query }),
	post: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
		data<T>('POST', path, { ...opts, body }),
	put: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
		data<T>('PUT', path, { ...opts, body }),
	patch: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
		data<T>('PATCH', path, { ...opts, body }),
	del: <T = void>(path: string, query?: RequestOptions['query'], opts?: RequestOptions) =>
		data<T>('DELETE', path, { ...opts, query }),
	/** Raw request returning the full JSON body (for non-standard envelopes). */
	raw: request
};

/** Returns a user-facing message for any error. */
export function errorMessage(e: unknown): string {
	if (e instanceof ApiError) return e.message;
	if (e instanceof Error) return e.message;
	return 'An unexpected error occurred.';
}

/** Generates a random idempotency key for provisioning requests. */
export function idempotencyKey(): string {
	return crypto.randomUUID();
}
