import { api } from '#lib/api/client.ts';
import type { PublicConfig } from '#lib/api/types.ts';

class ConfigState {
	value = $state<PublicConfig | null>(null);

	async load() {
		if (this.value) return this.value;
		try {
			this.value = await api.get<PublicConfig>('/config', undefined, { noAuthRedirect: true });
		} catch {
			this.value = null;
		}
		return this.value;
	}

	get name(): string {
		return this.value?.platform_name ?? 'ITK Cloud';
	}
}

export const appConfig = new ConfigState();
