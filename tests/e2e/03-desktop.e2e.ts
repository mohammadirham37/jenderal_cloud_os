// Desktop shell: windows, taskbar, start menu, persistence and embedded pages.
import { expect, test } from '@playwright/test';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { e2eAdmin, login } from './helpers.ts';
import { TotpSource } from './totp.ts';

// One TOTP source for the whole file: codes are single-use (replay protection).
let shared: (ReturnType<typeof e2eAdmin> & { totp: TotpSource }) | undefined;
function adminAccount() {
	if (!shared) {
		const st = JSON.parse(readFileSync(resolve(import.meta.dirname, '..', '..', '.dev', 'e2e', 'e2e-admin-mfa.json'), 'utf8'));
		shared = { ...e2eAdmin(), totp: new TotpSource(st.secret, st.lastStep) };
	}
	return shared;
}

test('desktop shell opens apps in windows and restores the layout', async ({ page }) => {
	const admin = adminAccount();
	const problems: string[] = [];
	page.on('pageerror', (e) => problems.push(e.message));

	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/login');
	await page.screenshot({ path: 'test-results/screens/desktop-0-lock.png' });
	await login(page, admin);
	await expect(page.getByRole('navigation', { name: 'Taskbar' })).toBeVisible();
	await page.waitForTimeout(900);
	await page.screenshot({ path: 'test-results/screens/desktop-1-empty.png' });

	// Open Terminal by double-clicking its desktop icon.
	await page.getByRole('button', { name: 'Open Terminal' }).dblclick();
	const term = page.locator('[data-window="terminal"]');
	await expect(term).toBeVisible();
	await expect(page.frameLocator('iframe[name^="itk-win:terminal"]').getByRole('heading', { level: 1, name: 'Terminal' })).toBeVisible();

	// Start menu search → Users.
	await page.getByRole('button', { name: 'Start' }).click();
	await page.getByPlaceholder('Type to search apps…').fill('users');
	await page.screenshot({ path: 'test-results/screens/desktop-2-start.png' });
	await page.keyboard.press('Enter');
	const users = page.locator('[data-window="users"]');
	await expect(users).toBeVisible();
	await expect(page.frameLocator('iframe[name^="itk-win:users"]').getByRole('heading', { level: 1, name: 'Users' })).toBeVisible();
	// Embedded pages have no classic sidebar.
	await expect(page.frameLocator('iframe[name^="itk-win:users"]').getByRole('navigation', { name: 'Main navigation' })).toHaveCount(0);

	// Open Monitoring from the start menu and take the hero screenshot.
	await page.getByRole('button', { name: 'Start' }).click();
	await page.getByRole('button', { name: /^Monitoring/ }).click();
	await expect(page.frameLocator('iframe[name^="itk-win:monitoring"]').getByRole('heading', { level: 1, name: 'Monitoring' })).toBeVisible();
	await page.waitForTimeout(1500);
	await page.screenshot({ path: 'test-results/screens/desktop-3-windows.png' });

	// Drag the monitoring window by its title bar.
	const bar = page.getByRole('toolbar', { name: /Monitoring title bar/ });
	const box = (await bar.boundingBox())!;
	await page.mouse.move(box.x + 200, box.y + 15);
	await page.mouse.down();
	await page.mouse.move(box.x + 60, box.y + 80, { steps: 8 });
	await page.mouse.up();
	const moved = (await bar.boundingBox())!;
	expect(Math.round(moved.y)).toBeGreaterThan(Math.round(box.y) + 40);

	// Minimize via the title bar, restore via the taskbar.
	await page.getByRole('button', { name: 'Minimize Users' }).click();
	await expect(users).toHaveAttribute('aria-hidden', 'true');
	await page.getByRole('button', { name: 'Users (open)' }).click();
	await expect(users).not.toHaveAttribute('aria-hidden', 'true');

	// Maximize and close.
	await page.getByRole('button', { name: 'Maximize Users' }).click();
	await page.getByRole('button', { name: 'Close Users' }).click();
	await expect(users).toHaveCount(0);

	// Cross-app link inside a window opens in the owning app's window.
	await page.keyboard.press('Control+Alt+d'); // show desktop (icons are behind windows)
	await page.getByRole('button', { name: 'Open Overview' }).dblclick();
	const overview = page.frameLocator('iframe[name^="itk-win:dashboard"]');
	await overview.getByRole('link', { name: 'View all' }).click();
	await expect(page.locator('[data-window="containers"]')).toBeVisible();

	// Layout is restored per user after a reload.
	await page.reload();
	await expect(page.locator('[data-window="terminal"]')).toBeVisible();
	await expect(page.locator('[data-window="monitoring"]')).toBeVisible();
	await expect(page.locator('[data-window="containers"]')).toBeVisible();

	// Right-click on the wallpaper → change wallpaper.
	await page.keyboard.press('Control+Alt+d');
	await page.mouse.click(700, 450, { button: 'right' });
	await page.getByRole('menuitem', { name: 'Wallpaper Fajar' }).click();
	await page.waitForTimeout(800);
	await page.screenshot({ path: 'test-results/screens/desktop-4-fajar.png' });
	expect(problems, problems.join('\n')).toEqual([]);
});

test('switches between OS mode and the classic console and remembers the choice', async ({ page }) => {
	await login(page, adminAccount());
	await expect(page.getByRole('navigation', { name: 'Taskbar' })).toBeVisible();

	// OS mode → classic via the start menu.
	await page.getByRole('button', { name: 'Start' }).click();
	await page.getByRole('link', { name: 'Classic console' }).click();
	await expect(page).toHaveURL(/\/dashboard$/);
	await expect(page.getByRole('link', { name: 'OS mode' })).toBeVisible();

	// The choice is remembered: opening "/" stays in the classic console.
	await page.goto('/');
	await expect(page).toHaveURL(/\/dashboard$/);
	await expect(page.getByRole('link', { name: 'OS mode' })).toBeVisible();

	// Classic → OS mode via the top bar.
	await page.getByRole('link', { name: 'OS mode' }).click();
	await expect(page.getByRole('navigation', { name: 'Taskbar' })).toBeVisible();
	await page.goto('/');
	await expect(page.getByRole('navigation', { name: 'Taskbar' })).toBeVisible();
});
