// Types for the system update page. Mirrors internal/modules/sysupdate.

export type UpdateJobState =
	'idle' | 'queued' | 'downloading' | 'verifying' | 'upgrading' | 'succeeded' | 'failed';

export interface UpdateRelease {
	version: string;
	tag: string;
	name: string;
	published_at: string;
	notes: string;
	html_url: string;
	asset: string;
	asset_available: boolean;
}

export interface UpdateJob {
	state: UpdateJobState;
	version: string;
	from_version: string;
	message: string;
	started_at: string;
	updated_at: string;
	finished_at: string;
	log: string[] | null;
}

export interface UpdateStatus {
	installed_version: string;
	enabled: boolean;
	repo: string;
	latest: UpdateRelease | null;
	checked_at: string | null;
	check_error: string;
	update_available: boolean;
	can_update: boolean;
	job: UpdateJob | null;
	job_error: string;
}

export const RUNNING_UPDATE_STATES: UpdateJobState[] = [
	'queued',
	'downloading',
	'verifying',
	'upgrading'
];
