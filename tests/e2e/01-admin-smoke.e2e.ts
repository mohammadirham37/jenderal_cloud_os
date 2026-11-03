// Administrator journey: first login with mandatory MFA enrollment, then a
// crawl of every console page checking for runtime errors and failed API calls.
import { expect, test, type Page } from '@playwright/test';
import { writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { e2eAdmin, login } from './helpers.ts';
import { TotpSource } from './totp.ts';

const pages = [
	'/dashboard', '/files', '/workspaces', '/workspaces/new', '/terminal', '/projects', '/containers', '/databases', '/gpu', '/templates',
	'/monitoring', '/monitoring?tab=history', '/monitoring?tab=gpu', '/monitoring?tab=services', '/monitoring?tab=alerts',
	'/monitoring?tab=jobs', '/notifications', '/account', '/account/password', '/admin/users', '/admin/roles',
	'/admin/quotas', '/admin/templates', '/admin/images', '/admin/host', '/admin/announcements', '/admin/audit',
	'/admin/backups', '/admin/settings'
];

function watch(page: Page) {
	const problems: string[] = [];
	page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`));
	page.on('console', (m) => {
		if (m.type() === 'error' && !/favicon|WebSocket/.test(m.text())) problems.push(`console: ${m.text()}`);
	});
	page.on('response', (r) => {
		if (r.url().includes('/api/') && r.status() >= 500) problems.push(`HTTP ${r.status()} ${r.url()}`);
	});
	return problems;
}

test('admin enrolls MFA on first login and every page renders without errors', async ({ page }) => {
	const admin = e2eAdmin();
	const problems = watch(page);

	await login(page, admin);
	await expect(page).toHaveURL(/\/mfa\/setup/);
	await page.getByText('Show setup URI').click();
	const uri = (await page.locator('code').first().textContent()) ?? '';
	const secret = new URL(uri.replace('otpauth://', 'https://')).searchParams.get('secret') ?? '';
	expect(secret).toMatch(/^[A-Z2-7]{16,}$/);
	const totp = new TotpSource(secret);
	await page.getByLabel('Authentication code').fill(await totp.next());
	await page.getByRole('button', { name: 'Enable two-factor authentication' }).click();
	await expect(page.getByRole('heading', { name: 'Save your recovery codes' })).toBeVisible();
	await page.getByLabel('I have stored these codes safely.').check();
	await page.getByRole('button', { name: 'Continue' }).click();
	await expect(page).toHaveURL(/\/$/);
	await expect(page.getByRole('navigation', { name: 'Taskbar' })).toBeVisible();
	writeFileSync(resolve(import.meta.dirname, '..', '..', '.dev', 'e2e', 'e2e-admin-mfa.json'), JSON.stringify({ secret, lastStep: totp.lastStep }));

	for (const p of pages) {
		await page.goto(p);
		await page.waitForLoadState('networkidle');
		await expect(page.locator('main h1').first(), `heading on ${p}`).toBeVisible();
		const errorStates = await page.getByText('Could not load data').count();
		expect(errorStates, `error state rendered on ${p}`).toBe(0);
		await page.screenshot({ path: `test-results/screens/${p.replace(/[/?=]/g, '_') || 'root'}.png`, fullPage: true });
	}
	expect(problems, problems.join('\n')).toEqual([]);
});
