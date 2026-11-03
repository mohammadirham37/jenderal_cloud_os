export type Theme = 'light' | 'dark' | 'system';

class ThemeState {
	value = $state<Theme>('system');

	init() {
		const saved = (localStorage.getItem('itk-theme') as Theme | null) ?? 'system';
		this.value = saved;
		this.apply();
		window
			.matchMedia('(prefers-color-scheme: dark)')
			.addEventListener('change', () => this.apply());
	}

	set(t: Theme) {
		this.value = t;
		localStorage.setItem('itk-theme', t);
		this.apply();
	}

	get isDark(): boolean {
		return (
			this.value === 'dark' ||
			(this.value === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches)
		);
	}

	apply() {
		document.documentElement.classList.toggle('dark', this.isDark);
	}
}

export const theme = new ThemeState();
