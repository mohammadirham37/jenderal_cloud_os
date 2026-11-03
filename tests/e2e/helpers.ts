import { expect, type Page } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { TotpSource } from './totp.ts';

export interface Account {
	username: string;
	password: string;
	totp?: TotpSource;
}

export function e2eAdmin(): Account & { run: string } {
	const p = resolve(import.meta.dirname, '..', '..', '.dev', 'e2e', 'e2e-admin.json');
	return JSON.parse(readFileSync(p, 'utf8'));
}

export async function login(page: Page, acct: Account) {
	await page.goto('/login');
	await page.getByLabel('Username or email').fill(acct.username);
	await page.locator('#password').fill(acct.password);
	await page.getByRole('button', { name: 'Sign in' }).click();
	if (acct.totp) {
		await expect(page).toHaveURL(/\/mfa$/);
		await page.getByLabel('Authentication code').fill(await acct.totp.next());
		await page.getByRole('button', { name: 'Verify' }).click();
	}
	await page.waitForURL((u) => !/\/(login|mfa)$/.test(u.pathname), { timeout: 30_000 });
	await page.locator('main').first().waitFor();
}

export async function logout(page: Page) {
	await page.getByRole('button', { name: /^[a-z0-9]{2}/i }).filter({ hasText: /./ }).last().click();
	await page.getByRole('menuitem', { name: 'Sign out' }).click();
	await expect(page).toHaveURL(/\/login/);
}

/** Calls the JSON API with the page's session (used for setup and assertions). */
export async function apiGet<T>(page: Page, path: string): Promise<T> {
	const res = await page.request.get(`/api/v1${path}`);
	expect(res.ok(), `GET ${path} -> ${res.status()}`).toBeTruthy();
	return ((await res.json()) as { data: T }).data;
}
