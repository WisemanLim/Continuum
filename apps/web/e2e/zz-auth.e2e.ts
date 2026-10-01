// Runs last (zz-): creating the first user switches the shared e2e Controller to authenticated mode.
import { expect, test } from "@playwright/test";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const shots = resolve(__dirname, "../../../test/impl/22/logs");

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("continuum.prefs", JSON.stringify({ lang: "en", theme: "light", units: "si" })));
});

// E22: bootstrap admin → login screen → admin creates an operator → operator is refused storage actions → logout.
test("login, users & roles, RBAC in the UI", async ({ page, request }) => {
  expect((await request.post("/v1/users", { data: { username: "admin", password: "admin-pass-123", role: "system-admin" } })).status()).toBe(201);

  await page.goto("/#/overview");
  await expect(page.getByRole("form", { name: "Log in" })).toBeVisible();
  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill("wrong-password");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByRole("alert")).toContainText("invalid username or password");
  await page.getByLabel("Password").fill("admin-pass-123");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByTestId("user-menu")).toHaveText("AD");

  await page.goto("/#/settings");
  await expect(page.getByRole("heading", { name: "Users & roles" })).toBeVisible();
  await page.getByLabel("New username").fill("ops");
  await page.getByLabel("New user's password").fill("operator-pass-1");
  await page.getByLabel("New user's role").selectOption("operator");
  await page.getByRole("button", { name: "Add user" }).click();
  await expect(page.getByTestId("users")).toContainText("ops");
  await page.screenshot({ path: join(shots, "01-users.png"), fullPage: true });

  await page.getByTestId("user-menu").click();
  await page.getByRole("button", { name: "Log out" }).click();
  await expect(page.getByRole("form", { name: "Log in" })).toBeVisible();
  await page.screenshot({ path: join(shots, "02-login.png"), fullPage: true });

  await page.getByLabel("Username").fill("ops");
  await page.getByLabel("Password").fill("operator-pass-1");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByTestId("user-menu")).toHaveText("OP");
  await page.goto("/#/settings");
  await expect(page.getByRole("heading", { name: "Users & roles" })).toHaveCount(0); // operator: no security permission
  await page.goto("/#/volumes/new");
  await page.getByLabel("Volume name").fill("Ops Volume");
  // operator cannot create volumes: the API answers 403 and the UI shows the reason
  const token = await page.evaluate(() => localStorage.getItem("continuum.token"));
  const r = await request.post("/v1/volumes", { data: { name: "x", disk_ids: [] }, headers: { Authorization: `Bearer ${token}` } });
  expect(r.status()).toBe(403);
  expect((await r.json()).error.message).toContain('lacks the "storage" permission');
  const audit = await request.get("/v1/audit?limit=20", { headers: { Authorization: `Bearer ${token}` } });
  expect(audit.status()).toBe(403);
});
