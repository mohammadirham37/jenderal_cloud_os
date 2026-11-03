// MVP journey (spec §24) in a real browser against the running stack:
// admin creates a user → user changes the temporary password → creates a
// project → creates a container workspace from a template → starts it →
// uses the browser terminal and file manager → another user cannot access it
// → the workspace is stopped → the administrator sees the audit trail.
import { expect, test, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { e2eAdmin, login } from './helpers.ts';
import { TotpSource } from './totp.ts';

test.describe.configure({ mode: 'serial' });

const admin = e2eAdmin();
let adminTotp: TotpSource | undefined;
const adminAcct = {
	...admin,
	get totp() {
		// Written by 01-admin-smoke after MFA enrollment.
		if (!adminTotp) {
			const st = JSON.parse(readFileSync(resolve(import.meta.dirname, '..', '..', '.dev', 'e2e', 'e2e-admin-mfa.json'), 'utf8'));
			adminTotp = new TotpSource(st.secret, st.lastStep);
		}
		return adminTotp;
	}
};
const student = { username: `mhs${admin.run}`, password: `Mahasiswa-${admin.run}-Pass1`, temp: '' };
const other = { username: `mhsb${admin.run}`, password: `Mahasiswa-${admin.run}-Pass2`, temp: '' };
let workspaceUrl = '';

async function createUser(page: Page, u: typeof student) {
	await page.goto('/admin/users');
	await page.getByRole('button', { name: 'New user' }).first().click();
	await page.locator('#cu-username').fill(u.username);
	await page.locator('#cu-email').fill(`${u.username}@itk.test`);
	await page.locator('#cu-name').fill(`Mahasiswa ${u.username}`);
	await page.getByRole('checkbox', { name: /Mahasiswa/ }).check();
	await page.getByRole('button', { name: 'Create user' }).click();
	await expect(page.getByRole('heading', { name: 'User created' })).toBeVisible();
	u.temp = (await page.locator('dialog code').first().textContent())?.trim() ?? '';
	expect(u.temp.length).toBeGreaterThanOrEqual(12);
}

async function firstLogin(page: Page, u: typeof student) {
	await login(page, { username: u.username, password: u.temp });
	await expect(page).toHaveURL(/\/account\/password/);
	await page.locator('input[autocomplete="current-password"]').fill(u.temp);
	const pw = page.locator('input[autocomplete="new-password"]');
	await pw.nth(0).fill(u.password);
	await pw.nth(1).fill(u.password);
	await page.getByRole('button', { name: /Change password|Update password/ }).click();
	await expect(page).toHaveURL(/\/$/);
}

test('administrator creates two student accounts', async ({ page }) => {
	await login(page, adminAcct);
	await expect(page.getByRole('navigation', { name: 'Taskbar' })).toBeVisible();
	await createUser(page, student);
	await page.keyboard.press('Escape');
	await createUser(page, other);
});

test('student changes the temporary password, creates a project and a workspace', async ({ page }) => {
	await firstLogin(page, student);
	await page.goto('/projects?new=1');
	await page.getByRole('dialog').getByLabel('Name').fill('Praktikum Jaringan');
	await page.getByRole('dialog').getByRole('button', { name: /Create/ }).click();
	await expect(page).toHaveURL(/\/projects\/[0-9a-f-]{36}/);

	await page.goto('/workspaces/new?template=ubuntu-dev');
	await page.locator('#ws-name').fill('Lab Linux');
	await page.getByRole('button', { name: 'Next' }).last().click();
	await page.locator('#ws-project').selectOption({ label: 'Praktikum Jaringan' });
	await page.getByRole('button', { name: 'Create workspace' }).click();
	await expect(page).toHaveURL(/\/workspaces\/[0-9a-f-]{36}$/);
	workspaceUrl = page.url();
	await expect(page.getByText('Ready', { exact: true }).first()).toBeVisible({ timeout: 60_000 });
	await page.getByRole('button', { name: 'Start' }).first().click();
	await expect(page.getByText('Running', { exact: true }).first()).toBeVisible({ timeout: 90_000 });
});

test('student uses the browser terminal and the file manager', async ({ page }) => {
	await login(page, student);
	const id = workspaceUrl.split('/').pop()!;
	await page.goto(`/terminal?workspace=${id}`);
	await page.getByRole('button', { name: 'Open terminal' }).click();
	const term = page.locator('.xterm').first();
	await expect(term).toBeVisible();
	await expect(page.getByText(/connected/i).first()).toBeVisible({ timeout: 30_000 });
	await term.click();
	await page.keyboard.type('echo "hasil=$((6*7))" > ~/catatan.txt && id -u\n');
	await expect(page.locator('.xterm-rows')).toContainText('1000', { timeout: 20_000 });

	await page.goto(`/workspaces/${id}/files`);
	await expect(page.getByText('catatan.txt')).toBeVisible({ timeout: 20_000 });
	await page.locator('input[type="file"]').first().setInputFiles({ name: 'tugas.py', mimeType: 'text/x-python', buffer: Buffer.from('print("Jenderal Cloud OS")\n') });
	await expect(page.getByText('tugas.py')).toBeVisible({ timeout: 20_000 });
	// fetch from the page (same origin, session cookie) to verify the download.
	const text = await page.evaluate(async (wid) => (await fetch(`/api/v1/workspaces/${wid}/files/download?path=catatan.txt`)).text(), id);
	expect(text).toBe('hasil=42\n');
});

test('another student cannot access the workspace', async ({ page }) => {
	await firstLogin(page, other);
	await page.goto(workspaceUrl);
	await expect(page.getByText(/not found/i).first()).toBeVisible();
	const id = workspaceUrl.split('/').pop()!;
	const status = await page.evaluate(async (wid) => (await fetch(`/api/v1/workspaces/${wid}/files`)).status, id);
	expect(status).toBe(404);
});

test('student stops the workspace; administrator sees the audit trail', async ({ page }) => {
	await login(page, student);
	await page.goto(workspaceUrl);
	await page.getByRole('button', { name: 'Stop' }).first().click();
	await page.getByRole('dialog').getByRole('button', { name: /Stop/ }).click();
	await expect(page.getByText('Stopped', { exact: true }).first()).toBeVisible({ timeout: 60_000 });
	await page.context().clearCookies();

	await login(page, adminAcct);
	await page.goto('/admin/audit');
	await expect(page.getByText('workspace.create').first()).toBeVisible();
	await expect(page.getByText('terminal.open').first()).toBeVisible();
	await expect(page.getByText('files.upload').first()).toBeVisible();
});
