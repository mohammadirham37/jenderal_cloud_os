// Types for the monitoring and administration area that are not in the
// shared types.ts. Mirrors the Go handlers in internal/modules/{monitoring,
// agent,hostops,images,backups,users} and packages/agentproto.
import type { AgentNode, HistoryPoint, HostMetrics, User } from './types.ts';

/** Last stored host sample returned when the agent is not responding. */
export interface HostLastSample {
	cpu_percent: number | null;
	load1: number | null;
	memory_used: number | null;
	memory_total: number | null;
	disk_used: number | null;
	disk_total: number | null;
	net_rx_bytes_per_sec: number | null;
	net_tx_bytes_per_sec: number | null;
	collected_at: string;
}

/** GET /monitoring/host */
export interface MonitoringHost {
	available: boolean;
	live: boolean;
	metrics?: HostMetrics;
	last_sample?: HostLastSample;
	error?: string;
	node: AgentNode | null;
}

/** GET /monitoring/history */
export interface MonitoringHistory {
	scope: string;
	ref: string;
	bucket_seconds: number;
	points: HistoryPoint[];
}

export type HistoryRange = '1h' | '6h' | '24h' | '7d' | '14d';

export interface GPUProcess {
	pid: number;
	name: string;
	memory_bytes: number;
	container_id?: string;
}

/** One GPU in GET /monitoring/gpu (agentproto.GPUDevice). */
export interface LiveGPU {
	index: number;
	uuid: string;
	name: string;
	driver_version: string;
	cuda_version?: string;
	memory_total_bytes: number | null;
	memory_used_bytes: number | null;
	utilization_pct: number | null;
	temperature_c: number | null;
	power_draw_w: number | null;
	mig_mode?: string;
	processes: GPUProcess[] | null;
}

/** GET /monitoring/gpu */
export interface GPUInventory {
	available: boolean;
	error?: string;
	gpus: LiveGPU[] | null;
}

export type ServiceState = 'up' | 'down' | 'degraded' | 'unknown' | 'not_configured';

export interface ServiceStatus {
	name: string;
	status: ServiceState;
	detail?: string;
	latency_ms?: number;
}

export interface QueueStats {
	queued: number;
	running: number;
	failed_24h: number;
	dead: number;
	oldest_queued_seconds: number;
}

/** GET /monitoring/services */
export interface ServicesOverview {
	services: ServiceStatus[];
	queue: QueueStats;
	last_backup: { status: string | null; finished_at: string | null; kind: string | null };
	containers_by_status: Record<string, number>;
	prometheus_url: string;
	grafana_url: string;
}

/** GET /monitoring/jobs */
export interface BackgroundJob {
	id: string;
	kind: string;
	status: 'dead' | 'queued' | 'running';
	attempts: number;
	max_attempts: number;
	last_error: string | null;
	resource_type: string | null;
	resource_id: string | null;
	created_at: string;
	updated_at: string;
}

/** GET /admin/agent/status */
export interface AgentStatusResponse {
	node?: AgentNode;
	live_error?: string;
	status?: string;
	error?: string;
	expected_protocol_version?: number;
}

/** POST /admin/host/emergency-stop result. */
export interface EmergencyStopResult {
	stopped?: number;
	failed?: number;
}

/** GET /images */
export interface ContainerImage {
	id: string;
	name: string;
	reference: string;
	description: string;
	gpu_capable: boolean;
	is_allowed: boolean;
	pulled_at: string | null;
	size_bytes: number | null;
	template_count: number;
	created_at: string;
	updated_at: string;
}

/** POST /users */
export interface CreateUserResponse {
	user: User;
	temporary_password?: string;
}

/** POST /users/{id}/reset-password */
export interface ResetPasswordResponse {
	reset_link: string;
	expires_in_seconds: number;
	emailed: boolean;
}

/** `config` object of GET /admin/backups. */
export interface BackupConfig {
	encryption_configured: boolean;
	offsite_copy_configured: boolean;
	retention_days: number;
	schedule_enabled: boolean;
	schedule_hour_utc: number;
	project_databases: boolean;
}

export type BackupKind =
	'platform_db' | 'config' | 'workspace_metadata' | 'workspace_files' | 'project_databases';
