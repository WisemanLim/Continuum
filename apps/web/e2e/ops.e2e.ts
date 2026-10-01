import { expect, test } from "@playwright/test";
import { mkdirSync, rmSync } from "node:fs";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const work = process.env.CONTINUUM_E2E_DIR!;
const shots = resolve(__dirname, "../../../test/impl/16/logs");

// English UI: selectors below use the English labels
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("continuum.prefs", JSON.stringify({ lang: "en", theme: "light", units: "si" })));
});

// E16: notification center, webhook settings + test, diagnostics download, /metrics (FR-G16·D26·O01·O04).
test("notifications, webhook, diagnostics, metrics", async ({ page, request }) => {
  const got: string[] = [];
  const hook = createServer((req, res) => {
    let b = "";
    req.on("data", (c) => (b += c));
    req.on("end", () => (got.push(b), res.end("ok")));
  });
  await new Promise<void>((r) => hook.listen(0, "127.0.0.1", () => r()));
  const hookURL = `http://127.0.0.1:${(hook.address() as AddressInfo).port}/hook`;

  const p = join(work, "disks", "nt-1");
  mkdirSync(p, { recursive: true });
  expect((await request.post("/v1/disks", { data: { path: p, name: "nt-1", min_free_bytes: 0 } })).status()).toBe(201);

  await page.goto("/#/settings");
  await page.getByLabel("Webhook URL").fill(hookURL);
  await page.getByLabel("Format").selectOption("slack");
  await page.getByRole("button", { name: "Save" }).nth(1).click();
  await expect(page.getByText("Notification settings saved")).toBeVisible();
  await page.getByRole("button", { name: "Send test" }).click();
  await expect(page.getByText("Test message sent")).toBeVisible();
  expect(got.some((b) => b.includes("Continuum: test notification"))).toBe(true);

  // disk goes offline → toast + unread badge + webhook
  const unreadBefore = Number((await page.getByTestId("unread").textContent({ timeout: 1000 }).catch(() => "0")) || 0);
  rmSync(p, { recursive: true, force: true });
  await request.post("/v1/disks/refresh", { data: {} });
  await expect(page.getByText("Disk nt-1 went offline — its files are shown as offline").first()).toBeVisible({ timeout: 20_000 });
  await expect(page.getByTestId("unread")).toHaveText(String(unreadBefore + 1));
  await page.getByRole("button", { name: /Notifications \(/ }).click();
  await expect(page.getByRole("dialog", { name: "Notifications" })).toContainText("nt-1 went offline");
  await page.screenshot({ path: join(shots, "01-notification-center.png"), fullPage: true });
  await page.getByRole("button", { name: "Mark all read" }).click();
  await expect(page.getByTestId("unread")).toHaveCount(0);
  await expect.poll(() => got.some((b) => b.includes("nt-1 went offline")), { timeout: 15_000 }).toBe(true);

  const [dl] = await Promise.all([page.waitForEvent("download"), page.getByRole("link", { name: "Download diagnostics bundle" }).click()]);
  expect(dl.suggestedFilename()).toMatch(/^continuum-diagnostics-\d{8}-\d{6}\.zip$/);
  await page.screenshot({ path: join(shots, "02-settings.png"), fullPage: true });

  const metrics = await (await request.get("/metrics")).text();
  expect(metrics).toContain('continuum_disk_up{disk="nt-1",volume="",ordinal="0"} 0');
  hook.close();
});
