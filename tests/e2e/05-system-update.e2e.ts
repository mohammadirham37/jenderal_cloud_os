// Administration → System update on a real server: the administrator checks
// GitHub for a newer release and installs it from the console.
//
// This upgrades the target server, so it only runs when E2E_SYSTEM_UPDATE=1.
// Needs 01-admin-smoke in the same run (MFA-enrolled administrator).
import { expect, test } from "@playwright/test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { e2eAdmin, login } from "./helpers.ts";
import { TotpSource } from "./totp.ts";

test.skip(
  process.env.E2E_SYSTEM_UPDATE !== "1",
  "set E2E_SYSTEM_UPDATE=1 to upgrade the target server",
);

test("administrator installs the latest release from the console", async ({
  page,
}) => {
  test.setTimeout(1_200_000);
  const st = JSON.parse(
    readFileSync(
      resolve(
        import.meta.dirname,
        "..",
        "..",
        ".dev",
        "e2e",
        "e2e-admin-mfa.json",
      ),
      "utf8",
    ),
  );
  await login(page, {
    ...e2eAdmin(),
    totp: new TotpSource(st.secret, st.lastStep),
  });

  await page.goto("/admin/updates");
  await page.getByRole("button", { name: "Check now" }).click();
  const update = page.getByRole("button", { name: /^Update to v/ });
  await expect(update).toBeVisible({ timeout: 60_000 });
  const target = ((await update.textContent()) ?? "")
    .trim()
    .replace(/^Update to v/, "");
  await page.screenshot({ path: "test-results/screens/update-available.png" });

  await update.click();
  const dialog = page.getByRole("dialog");
  await dialog
    .locator("#confirm-reason")
    .fill("E2E: verify console self-update");
  await dialog.locator("#confirm-text").fill(`v${target}`);
  await dialog.getByRole("button", { name: `Update to v${target}` }).click();

  // The console goes away while services restart; the page keeps polling.
  await expect(
    page.getByRole("button", { name: "Reload console" }),
  ).toBeVisible({ timeout: 900_000 });
  await page.screenshot({ path: "test-results/screens/update-done.png" });
  const ready = await page.request.get("/health/ready");
  expect(((await ready.json()) as { version: string }).version).toBe(target);
});
