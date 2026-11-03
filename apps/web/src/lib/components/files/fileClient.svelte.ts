// File manager API wrapper for one workspace. Adds the emergency access
// reason (asked once per page) to every request and implements uploads with
// XMLHttpRequest for progress reporting.
import { api, ApiError, buildUrl, type RequestOptions } from '#lib/api/client.ts';
import type { FileEntry } from '#lib/api/types.ts';
import { auth } from '#lib/stores/auth.svelte.ts';
import { ACCESS_REASON_HEADER, askEmergencyReason, isEmergencyRequired } from './emergency.ts';

export interface DirListing {
	path: string;
	entries: FileEntry[];
	truncated: boolean;
}

export interface FileText {
	path: string;
	content: string;
	size: number;
	mod_time: string;
}

export class FileClient {
	readonly workspaceId: string;
	/** Emergency access justification; empty for normal access. */
	reason = $state('');
	#asking: Promise<string | null> | null = null;

	constructor(workspaceId: string) {
		this.workspaceId = workspaceId;
	}

	get base(): string {
		return `/workspaces/${this.workspaceId}/files`;
	}

	#opts(): RequestOptions {
		return this.reason ? { headers: { [ACCESS_REASON_HEADER]: this.reason } } : {};
	}

	/** Asks for the reason once, even when several requests fail concurrently. */
	async #ensureReason(): Promise<boolean> {
		if (!this.#asking) {
			this.#asking = askEmergencyReason('work with its files').finally(() => {
				this.#asking = null;
			});
		}
		const r = await this.#asking;
		if (r) this.reason = r;
		return !!this.reason;
	}

	async #call<T>(fn: (opts: RequestOptions) => Promise<T>): Promise<T> {
		try {
			return await fn(this.#opts());
		} catch (e) {
			if (isEmergencyRequired(e) && (await this.#ensureReason())) return fn(this.#opts());
			throw e;
		}
	}

	list(path: string): Promise<DirListing> {
		return this.#call((o) => api.get<DirListing>(this.base, { path }, o));
	}

	read(path: string): Promise<FileText> {
		return this.#call((o) => api.get<FileText>(`${this.base}/content`, { path }, o));
	}

	write(
		path: string,
		content: string,
		opts: { expectedModTime?: string; create?: boolean } = {}
	): Promise<FileEntry> {
		const body: Record<string, unknown> = { path, content };
		if (opts.expectedModTime) body.expected_mod_time = opts.expectedModTime;
		if (opts.create) body.create = true;
		return this.#call((o) => api.put<FileEntry>(`${this.base}/content`, body, o));
	}

	mkdir(path: string): Promise<FileEntry> {
		return this.#call((o) => api.post<FileEntry>(`${this.base}/mkdir`, { path }, o));
	}

	rename(from: string, to: string): Promise<FileEntry> {
		return this.#call((o) => api.patch<FileEntry>(`${this.base}/rename`, { from, to }, o));
	}

	remove(path: string, recursive: boolean): Promise<void> {
		return this.#call((o) =>
			api.del(this.base, { path, recursive: recursive ? 'true' : undefined }, o)
		);
	}

	downloadUrl(path: string): string {
		return buildUrl(`${this.base}/download`, { path });
	}

	/**
	 * Downloads with the emergency header (a plain link cannot send it).
	 * Only used under emergency access; normal downloads use a link.
	 */
	async downloadWithReason(path: string, name: string): Promise<void> {
		const res = await fetch(this.downloadUrl(path), {
			headers: this.#opts().headers,
			credentials: 'same-origin'
		});
		if (!res.ok) {
			let body: { error?: { code?: string; message?: string } } = {};
			try {
				body = await res.json();
			} catch {
				// not JSON
			}
			throw new ApiError(res.status, body.error ?? {});
		}
		const url = URL.createObjectURL(await res.blob());
		const a = document.createElement('a');
		a.href = url;
		a.download = name;
		document.body.append(a);
		a.click();
		a.remove();
		setTimeout(() => URL.revokeObjectURL(url), 30000);
	}

	/** Uploads one file into `dir`; reports progress (0..1). */
	async upload(
		file: File,
		dir: string,
		overwrite: boolean,
		onprogress: (fraction: number) => void,
		signal?: AbortSignal
	): Promise<FileEntry[]> {
		try {
			return await this.#xhrUpload(file, dir, overwrite, onprogress, signal);
		} catch (e) {
			if (isEmergencyRequired(e) && (await this.#ensureReason())) {
				return this.#xhrUpload(file, dir, overwrite, onprogress, signal);
			}
			throw e;
		}
	}

	#xhrUpload(
		file: File,
		dir: string,
		overwrite: boolean,
		onprogress: (fraction: number) => void,
		signal?: AbortSignal
	): Promise<FileEntry[]> {
		return new Promise((resolve, reject) => {
			const xhr = new XMLHttpRequest();
			const url = buildUrl(`${this.base}/upload`, {
				path: dir,
				overwrite: overwrite ? 'true' : undefined
			});
			xhr.open('POST', url);
			xhr.responseType = 'text';
			xhr.setRequestHeader('Accept', 'application/json');
			const csrf = auth.me?.csrf_token;
			if (csrf) xhr.setRequestHeader('X-CSRF-Token', csrf);
			if (this.reason) xhr.setRequestHeader(ACCESS_REASON_HEADER, this.reason);
			xhr.upload.onprogress = (e) => {
				if (e.lengthComputable && e.total > 0) onprogress(e.loaded / e.total);
			};
			xhr.onload = () => {
				let json: { data?: FileEntry[]; error?: { code?: string; message?: string } };
				try {
					json = xhr.responseText ? JSON.parse(xhr.responseText) : {};
				} catch {
					json = {};
				}
				if (xhr.status >= 200 && xhr.status < 300) {
					onprogress(1);
					resolve(json.data ?? []);
				} else {
					reject(new ApiError(xhr.status, json.error ?? {}));
				}
			};
			xhr.onerror = () =>
				reject(
					new ApiError(0, {
						code: 'network_error',
						message: 'The upload failed because the server could not be reached.'
					})
				);
			xhr.onabort = () =>
				reject(new ApiError(0, { code: 'aborted', message: 'Upload cancelled.' }));
			signal?.addEventListener('abort', () => xhr.abort(), { once: true });
			const fd = new FormData();
			fd.append('file', file, file.name);
			xhr.send(fd);
		});
	}
}
