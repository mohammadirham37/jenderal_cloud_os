import { defineConfig, devices } from '@playwright/test';

// Browser end-to-end tests live in tests/e2e at the repository root. They
// run against a running ITK Cloud stack (agent + API + worker + web), e.g.
// the local development stack (`make dev-agent dev-api dev-worker dev-web`).
// Set E2E_BASE_URL to target another deployment.
export default defineConfig({
	testDir: '../../tests/e2e',
	testMatch: '**/*.e2e.ts',
	fullyParallel: false,
	workers: 1,
	timeout: 180_000,
	expect: { timeout: 15_000 },
	retries: 0,
	reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
	globalSetup: '../../tests/e2e/global-setup.ts',
	use: {
		baseURL: process.env.E2E_BASE_URL ?? 'http://localhost:5173',
		trace: 'retain-on-failure',
		screenshot: 'only-on-failure',
		ignoreHTTPSErrors: true
	},
	projects: [
		{
			name: 'chromium',
			use: {
				...devices['Desktop Chrome'],
				// E2E_RESOLVE="itk-cloud.local=127.0.0.1" maps the console domain
				// to a staging server without editing /etc/hosts.
				launchOptions: process.env.E2E_RESOLVE
					? { args: [`--host-resolver-rules=MAP ${process.env.E2E_RESOLVE.replace('=', ' ')}`] }
					: {}
			}
		}
	]
});
