// Built-in workspace apps on a real server: the remote web browser (Firefox
// over noVNC) and VS Code (code-server), both opened from the desktop shell
// through the isolated app-proxy origin.
//
// Needs an existing non-admin account with workspace quota:
//   E2E_APPS_USER / E2E_APPS_PASSWORD
// and a server with the desktop-browser image and code-server installed.
import { expect, test, type Page } from "@playwright/test";
import { login } from "./helpers.ts";

const user = process.env.E2E_APPS_USER ?? "";
const password = process.env.E2E_APPS_PASSWORD ?? "";

test.describe.configure({ mode: "serial" });
test.skip(!user || !password, "E2E_APPS_USER / E2E_APPS_PASSWORD not set");

async function api(page: Page, method: string, path: string, body?: unknown) {
  const me = await page.request.get("/api/v1/auth/me");
  const csrf = ((await me.json()) as { data: { csrf_token: string } }).data
    .csrf_token;
  const res = await page.request.fetch(`/api/v1${path}`, {
    method,
    data: body,
    headers: { "X-CSRF-Token": csrf, "Idempotency-Key": crypto.randomUUID() },
  });
  return { status: res.status(), body: await res.json().catch(() => null) };
}

type Ws = { id: string; status: string; template_key: string };

async function mine(page: Page): Promise<Ws[]> {
  const list = await page.request.get(
    "/api/v1/workspaces?mine=true&per_page=100",
  );
  return ((await list.json()) as { data: Ws[] }).data;
}

/** Stops running workspaces (frees memory on small servers). */
async function stopAll(page: Page) {
  for (const w of await mine(page)) {
    if (w.status === "running")
      await api(page, "POST", `/workspaces/${w.id}/stop`);
  }
}

/** Deletes the test account's development workspaces from earlier runs (frees storage quota). */
async function deleteDevWorkspaces(page: Page) {
  for (const w of await mine(page)) {
    if (
      w.template_key !== "browser" &&
      !["deleting", "deleted"].includes(w.status)
    ) {
      await api(page, "DELETE", `/workspaces/${w.id}`);
    }
  }
  await expect
    .poll(
      async () =>
        (await mine(page)).filter(
          (w) => w.template_key !== "browser" && w.status !== "deleted",
        ).length,
      {
        timeout: 120_000,
      },
    )
    .toBe(0);
}

test("web browser app streams Firefox into a desktop window", async ({
  page,
}) => {
  test.setTimeout(420_000);
  await login(page, { username: user, password });
  await deleteDevWorkspaces(page);
  await stopAll(page);
  await page.goto("/browser");
  const create = page.getByRole("button", { name: "Create my browser" });
  const start = page.getByRole("button", { name: "Start workspace" });
  await expect(
    create.or(start).or(page.locator('iframe[title="Web Browser"]')),
  ).toBeVisible({ timeout: 30_000 });
  if (await create.isVisible()) await create.click();
  else if (await start.isVisible()) await start.click();
  const frame = page.locator('iframe[title="Web Browser"]');
  await expect(frame).toBeVisible({ timeout: 300_000 });
  // noVNC connects and draws the remote screen on a canvas.
  const vnc = page.frameLocator('iframe[title="Web Browser"]');
  await expect(vnc.locator("canvas").first()).toBeVisible({ timeout: 120_000 });
  await expect(vnc.locator("#noVNC_status, .noVNC_status"))
    .not.toContainText(/failed|disconnected/i, { timeout: 5_000 })
    .catch(() => {});
  await page.waitForTimeout(8_000); // let Firefox paint its first page
  await page.screenshot({ path: "test-results/screens/apps-browser.png" });
});

test("VS Code opens a development workspace", async ({ page }) => {
  test.setTimeout(420_000);
  await login(page, { username: user, password });
  await stopAll(page);
  const created = await api(page, "POST", "/workspaces", {
    name: `Kode ${Date.now() % 100000}`,
    template_key: "ubuntu-dev",
    storage_bytes: 3 * 1024 ** 3,
  });
  expect(created.status, JSON.stringify(created.body)).toBe(202);
  const id = (created.body as { data: { id: string } }).data.id;
  await page.goto(`/ide?workspace=${id}`);
  const frame = page.locator('iframe[title="VS Code"]');
  const start = page.getByRole("button", { name: "Start workspace" });
  await expect(frame.or(start)).toBeVisible({ timeout: 300_000 });
  if (await start.isVisible()) {
    await start.click();
    await expect(frame).toBeVisible({ timeout: 300_000 });
  }
  const code = page.frameLocator('iframe[title="VS Code"]');
  await expect(code.locator(".monaco-workbench")).toBeVisible({
    timeout: 180_000,
  });
  await page.waitForTimeout(5_000);
  await page.screenshot({ path: "test-results/screens/apps-vscode.png" });
});
