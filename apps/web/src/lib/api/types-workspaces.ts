// Types for the workspaces / containers / templates area that are not in the
// shared types.ts. Mirrors the Go handlers in internal/modules/{workspaces,
// containers,monitoring,resources,users}.
import type { Container, HistoryPoint, Quota, Usage } from './types.ts';

/** GET /me/usage */
export interface MyUsage {
	quota: Quota;
	usage: Usage;
	/** Workspaces counted toward the quota, largest storage first. */
	workspaces: UsageWorkspace[];
	/** Cap on the storage pre-filled for new workspaces (0 = template default). */
	default_storage_cap_bytes: number;
}

/** One workspace in GET /me/usage (or /users/{id}/usage). */
export interface UsageWorkspace {
	id: string;
	name: string;
	status: string;
	template_name: string;
	storage_bytes: number;
	storage_used_bytes: number | null;
	cpu_millis: number;
	memory_bytes: number;
}

/** GET /users/lookup?q= */
export interface UserLookup {
	id: string;
	username: string;
	full_name: string;
}

/** GET /workspaces/{id}/jobs */
export interface WorkspaceJob {
	id: string;
	kind: string;
	status: string;
	attempts: number;
	last_error: string | null;
	created_at: string;
	finished_at: string | null;
}

/** Per-container live stats in GET /monitoring/workspaces/{id}. */
export interface ContainerLiveStats {
	container_id: string;
	name: string;
	cpu_percent: number;
	memory_bytes: number;
	memory_limit: number;
	pids: number;
	net_rx_bytes: number;
	net_tx_bytes: number;
}

/** GET /monitoring/workspaces/{id} */
export interface WorkspaceMetrics {
	workspace_id: string;
	storage_bytes: number;
	storage_used_bytes: number | null;
	cpu_limit_millis: number;
	memory_limit_bytes: number;
	containers: ContainerLiveStats[];
	error?: string;
}

/** GET /monitoring/history */
export interface HistoryResponse {
	scope: string;
	ref: string;
	bucket_seconds: number;
	points: HistoryPoint[];
}

/** Runtime state from the agent (container inspect). */
export interface ContainerRuntime {
	runtime_id: string;
	status: string;
	exit_code: number;
	error?: string;
	started_at?: string;
	finished_at?: string;
	oom_killed: boolean;
	host_ports?: Record<string, number>;
}

export interface ContainerStats {
	runtime_id: string;
	cpu_percent: number;
	memory_bytes: number;
	memory_limit: number;
	pids: number;
	net_rx_bytes: number;
	net_tx_bytes: number;
}

/** GET /containers/{id} */
export interface ContainerDetail extends Container {
	runtime: ContainerRuntime | null;
	runtime_error?: string;
	stats: ContainerStats | null;
}

/** GET /containers/{id}/logs */
export interface LogsResult {
	lines: string[];
	truncated: boolean;
}
