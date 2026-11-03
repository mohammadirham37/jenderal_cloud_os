// API response types (mirrors the Go structs in internal/modules/*).

export interface Me {
	id: string;
	username: string;
	full_name: string;
	email: string;
	roles: string[];
	permissions: string[];
	mfa_enrolled: boolean;
	mfa_required: boolean;
	mfa_verified: boolean;
	mfa_setup_required: boolean;
	must_change_password: boolean;
	csrf_token: string;
	session_expires_at: string;
}

export interface PublicConfig {
	platform_name: string;
	version: string;
	app_proxy_origin: string;
	upload_max_bytes: number;
	editor_max_bytes: number;
	gpu_enabled: boolean;
	native_workspaces: boolean;
	project_databases: boolean;
	email_enabled: boolean;
	terminal_ttl_minutes: number;
}

export interface User {
	id: string;
	username: string;
	email: string;
	full_name: string;
	identity_number: string | null;
	status: 'active' | 'suspended';
	suspended_reason: string | null;
	must_change_password: boolean;
	roles: string[];
	quota_id: string | null;
	quota_key: string | null;
	mfa_enabled: boolean;
	last_login_at: string | null;
	locked_until: string | null;
	created_at: string;
	updated_at: string;
}

export interface Role {
	id: string;
	key: string;
	name: string;
	description: string;
	is_system: boolean;
	requires_mfa: boolean;
	rank: number;
	permissions: string[];
	user_count: number;
}

export interface Permission {
	key: string;
	category: string;
	description: string;
}

export interface Quota {
	id: string;
	key: string;
	name: string;
	description: string;
	cpu_millis: number;
	memory_bytes: number;
	storage_bytes: number;
	max_workspaces: number;
	max_containers: number;
	max_databases: number;
	max_terminal_sessions: number;
	gpu_allowed: boolean;
	max_workspace_duration_min: number | null;
	idle_timeout_min: number | null;
	user_count: number;
	project_count: number;
}

export interface Usage {
	workspaces: number;
	containers: number;
	databases: number;
	terminal_sessions: number;
	running_cpu_millis: number;
	running_memory_bytes: number;
	allocated_storage_bytes: number;
	used_storage_bytes: number;
	active_gpu_allocations: number;
}

export type WorkspaceStatus =
	| 'requested'
	| 'provisioning'
	| 'ready'
	| 'running'
	| 'stopped'
	| 'suspended'
	| 'failed'
	| 'deleting'
	| 'deleted';

export interface Port {
	port: number;
	name: string;
	proxy?: boolean;
	host_port?: number;
}

export interface Template {
	id: string;
	key: string;
	name: string;
	description: string;
	category: string;
	image_id: string;
	image_reference: string;
	image_allowed: boolean;
	image_present: boolean;
	runtime_versions: Record<string, string>;
	min_cpu_millis: number;
	min_memory_bytes: number;
	rec_cpu_millis: number;
	rec_memory_bytes: number;
	default_storage_bytes: number;
	ports: Port[];
	gpu_mode: 'none' | 'optional' | 'required';
	security: { run_as_uid: number; read_only_rootfs: boolean };
	env: Record<string, string>;
	command: string[];
	is_active: boolean;
}

export interface Workspace {
	id: string;
	name: string;
	slug: string;
	owner_id: string;
	owner_username: string;
	project_id: string | null;
	project_name: string | null;
	template_id: string;
	template_key: string;
	template_name: string;
	type: 'container' | 'native';
	status: WorkspaceStatus;
	status_reason: string | null;
	cpu_millis: number;
	memory_bytes: number;
	storage_bytes: number;
	pids_limit: number;
	idle_timeout_min: number | null;
	max_duration_min: number | null;
	storage_used_bytes: number | null;
	last_activity_at: string | null;
	started_at: string | null;
	created_at: string;
	updated_at: string;
	access?: 'owner' | 'editor' | 'operator' | 'viewer' | 'none';
	pending_job: string | null;
}

export interface Grants {
	View: boolean;
	Operate: boolean;
	Edit: boolean;
	Manage: boolean;
	Emergency: boolean;
}

export interface Container {
	id: string;
	workspace_id: string;
	workspace_name: string;
	owner_id: string;
	owner_username: string;
	template_id: string;
	template_name: string;
	image_id: string;
	image: string;
	name: string;
	is_primary: boolean;
	runtime_id: string | null;
	status: string;
	status_reason: string | null;
	cpu_millis: number;
	memory_bytes: number;
	pids_limit: number;
	ports: Port[];
	gpu_device_ids: string[];
	started_at: string | null;
	created_at: string;
	updated_at: string;
	pending_job: string | null;
}

export interface WorkspaceMember {
	user_id: string;
	username: string;
	full_name: string;
	role: 'viewer' | 'editor';
}

export interface WorkspaceDetail extends Workspace {
	containers: Container[];
	template: Template;
	members: WorkspaceMember[];
	grants: Grants;
}

export interface Project {
	id: string;
	slug: string;
	name: string;
	description: string;
	owner_id: string;
	owner_username: string;
	quota_id: string | null;
	quota_key: string | null;
	status: 'active' | 'archived';
	member_count: number;
	workspace_count: number;
	database_count: number;
	my_role: 'owner' | 'maintainer' | 'member' | 'viewer' | null;
	created_at: string;
	updated_at: string;
}

export interface ProjectMember {
	user_id: string;
	username: string;
	full_name: string;
	role: 'owner' | 'maintainer' | 'member' | 'viewer';
	created_at: string;
}

export interface Database {
	id: string;
	project_id: string;
	project_name: string;
	created_by: string;
	created_by_username: string;
	display_name: string;
	db_name: string;
	role_name: string;
	engine: string;
	status: 'provisioning' | 'ready' | 'failed' | 'deleting' | 'deleted';
	status_reason: string | null;
	size_bytes: number | null;
	connection_limit: number;
	last_backup_at: string | null;
	last_backup_status: string | null;
	host: string;
	port: number;
	created_at: string;
}

export interface DatabaseCredentials {
	host: string;
	port: number;
	database: string;
	username: string;
	password: string;
	connection_string: string;
}

export interface FileEntry {
	name: string;
	path: string;
	type: 'file' | 'dir' | 'symlink' | 'other';
	size: number;
	mode: string;
	mod_time: string;
}

export interface TerminalSession {
	id: string;
	user_id: string;
	workspace_id: string;
	workspace_name: string;
	container_id: string | null;
	container_name: string | null;
	status: 'pending' | 'active' | 'closed' | 'expired';
	created_at: string;
	connected_at: string | null;
	last_activity_at: string | null;
	expires_at: string;
	ended_at: string | null;
	end_reason: string | null;
	websocket_path: string;
}

export interface GPUDevice {
	id: string;
	uuid: string;
	index: number;
	name: string;
	driver_version: string | null;
	cuda_version: string | null;
	memory_total_bytes: number | null;
	memory_used_bytes: number | null;
	utilization_pct: number | null;
	temperature_c: number | null;
	mig_mode: string | null;
	allocation_policy: 'exclusive' | 'shared' | 'disabled';
	max_shared: number;
	status: 'available' | 'missing' | 'disabled';
	active_allocations: number;
	last_seen_at: string;
}

export interface GPURequest {
	id: string;
	requester_id: string;
	requester_username: string;
	workspace_id: string;
	workspace_name: string;
	gpu_device_id: string | null;
	gpu_name: string | null;
	mode: 'exclusive' | 'shared';
	duration_hours: number;
	reason: string;
	status: 'pending' | 'approved' | 'rejected' | 'cancelled';
	reviewed_by_username: string | null;
	review_note: string | null;
	reviewed_at: string | null;
	created_at: string;
}

export interface GPUAllocation {
	id: string;
	gpu_device_id: string;
	gpu_name: string;
	gpu_index: number;
	workspace_id: string;
	workspace_name: string;
	user_id: string;
	username: string;
	mode: string;
	status: 'active' | 'released' | 'expired';
	starts_at: string;
	ends_at: string;
	released_at: string | null;
	release_reason: string | null;
}

export interface Notification {
	id: string;
	type: string;
	severity: 'info' | 'success' | 'warning' | 'critical';
	title: string;
	body: string;
	link: string | null;
	read_at: string | null;
	created_at: string;
}

export interface Announcement {
	id: string;
	title: string;
	body: string;
	severity: 'info' | 'warning' | 'critical';
	starts_at: string;
	ends_at: string | null;
	created_by_username: string | null;
	created_at: string;
}

export interface AuditLog {
	id: number;
	occurred_at: string;
	actor_id: string | null;
	actor_username: string | null;
	action: string;
	resource_type: string;
	resource_id: string;
	outcome: 'success' | 'failure' | 'denied';
	reason: string | null;
	ip: string | null;
	user_agent: string | null;
	request_id: string | null;
	metadata: Record<string, unknown>;
}

export interface BackupJob {
	id: string;
	kind: string;
	status: 'queued' | 'running' | 'succeeded' | 'failed' | 'expired';
	trigger: 'manual' | 'scheduled' | 'pre_upgrade';
	file_name: string | null;
	size_bytes: number | null;
	checksum_sha256: string | null;
	encrypted: boolean;
	error: string | null;
	triggered_by_username: string | null;
	started_at: string | null;
	finished_at: string | null;
	expires_at: string | null;
	restore_status: string | null;
	restored_at: string | null;
	restore_error: string | null;
	created_at: string;
}

export interface Setting {
	key: string;
	type: 'bool' | 'int' | 'string' | 'enum';
	default: unknown;
	min?: number;
	max?: number;
	options?: string[];
	description: string;
	category: string;
	value: unknown;
}

export interface Alert {
	id: string;
	rule: string;
	subject: string;
	severity: 'info' | 'warning' | 'critical';
	status: 'firing' | 'resolved';
	message: string;
	value: number | null;
	started_at: string;
	resolved_at: string | null;
	acknowledged_by_username: string | null;
	acknowledged_at: string | null;
}

export interface HostMetrics {
	collected_at: string;
	cpu_percent: number;
	per_core: number[] | null;
	load1: number;
	load5: number;
	load15: number;
	memory_total: number;
	memory_used: number;
	memory_available: number;
	swap_total: number;
	swap_used: number;
	disks: { mountpoint: string; fstype: string; total: number; used: number; free: number }[] | null;
	net_rx_bytes_per_sec: number;
	net_tx_bytes_per_sec: number;
	uptime_seconds: number;
	docker: {
		available: boolean;
		version?: string;
		error?: string;
		containers_running: number;
		containers_stopped: number;
		containers_paused: number;
		images: number;
	};
}

export interface AgentNode {
	name: string;
	hostname: string | null;
	agent_version: string | null;
	protocol_version: number | null;
	os: string | null;
	kernel: string | null;
	arch: string | null;
	capabilities: {
		docker: boolean;
		docker_version?: string;
		docker_error?: string;
		nvidia: boolean;
		nvidia_runtime: boolean;
		native_workspaces: boolean;
		storage_driver: string;
		storage_enforced: boolean;
		apparmor: boolean;
		seccomp: boolean;
	};
	host_info: {
		cpu_model: string;
		cpu_cores: number;
		cpu_threads: number;
		memory_total: number;
		boot_time: string;
	};
	status: 'online' | 'offline' | 'degraded' | 'unknown';
	last_error: string | null;
	last_seen_at: string | null;
	expected_protocol_version: number;
}

export interface HistoryPoint {
	t: string;
	cpu_pct: number | null;
	memory_bytes: number | null;
	memory_total_bytes: number | null;
	disk_used_bytes: number | null;
	net_rx_bytes_per_sec: number | null;
	net_tx_bytes_per_sec: number | null;
	load1: number | null;
	gpu_util_pct: number | null;
	gpu_memory_bytes: number | null;
	gpu_temperature_c: number | null;
}
