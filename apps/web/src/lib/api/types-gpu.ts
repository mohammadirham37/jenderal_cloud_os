// GPU Center response shapes not covered by types.ts
// (mirrors internal/modules/gpu/gpu.go and packages/agentproto).
import type { GPUDevice } from './types.ts';

export interface GPUProcess {
	pid: number;
	name: string;
	memory_bytes: number;
	/** ITK container UUID when attributable. */
	container_id?: string;
}

export interface GPUInventory {
	devices: GPUDevice[];
	gpu_enabled: boolean;
	nvidia_driver: boolean;
	nvidia_container_runtime: boolean;
	/** Keyed by GPU UUID; only present for gpu.manage / gpu.approve / monitoring.view_host. */
	processes?: Record<string, GPUProcess[] | null>;
	inventory_error?: string;
}
