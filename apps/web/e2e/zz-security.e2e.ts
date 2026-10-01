// Runs after zz-auth (accounts exist): TOTP enrolment + MFA login, two-person approval in the UI (E29).
import { expect, test, type APIRequestContext } from "@playwright/test";
import { createHmac } from "node:crypto";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const shots = resolve(__dirname, "../../../test/impl/29/logs");

function totp(secret: string, step: number): string {
  const alpha = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const ch of secret.replace(/=+$/, "")) bits += alpha.indexOf(ch).toString(2).padStart(5, "0");
  const key = Buffer.from(bits.match(/.{8}/g)!.map((b) => parseInt(b, 2)));
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(step));
  const h = createHmac("sha1", key).update(msg).digest();
  const o = h[h.length - 1] & 15;
  return String((h.readUInt32BE(o) & 0x7fffffff) % 1_000_000).padStart(6, "0");
}

async function login(request: APIRequestContext, username: string, password: string, code = "") {
  const r = await request.post("/v1/auth/login", { data: { username, password, code } });
  expect(r.status()).toBe(200);
  return (await r.json()).token as string;
}

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("continuum.prefs", JSON.stringify({ lang: "en", theme: "light", units: "si" })));
});

test("MFA enrolment and login with an authenticator code", async ({ page, request }) => {
  const admin = await login(request, "admin", "admin-pass-123");
  await page.addInitScript((t) => localStorage.setItem("continuum.token", t), admin);
  await page.goto("/#/settings");
  const card = page.getByTestId("mfa");
  await expect(card).toContainText("Off");
  await card.getByRole("button", { name: "Set up" }).click();
  const secret = (await page.getByTestId("mfa-secret").textContent())!.trim();
  const step = Math.floor(Date.now() / 30000);
  await card.getByLabel("Authenticator code").fill(totp(secret, step));
  await card.getByRole("button", { name: "Enable" }).click();
  await expect(page.getByTestId("mfa")).toContainText("Enabled", { timeout: 10000 });
  await page.screenshot({ path: join(shots, "01-mfa-enabled.png"), fullPage: true });

  await page.evaluate(() => localStorage.removeItem("continuum.token"));
  await page.addInitScript(() => localStorage.removeItem("continuum.token"));
  await page.goto("/#/overview");
  await page.getByLabel("Username").fill("admin");
  await page.getByLabel("Password").fill("admin-pass-123");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByLabel("Authenticator code")).toBeVisible();
  await page.screenshot({ path: join(shots, "02-mfa-prompt.png"), fullPage: true });
  await page.getByLabel("Authenticator code").fill("000000");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByRole("alert")).toContainText("invalid username or password");
  await page.getByLabel("Authenticator code").fill(totp(secret, step + 1)); // the enable code is spent (replay guard)
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByTestId("user-menu")).toHaveText("AD");
  // turn MFA off again so the next spec can log in with a password only
  await page.goto("/#/settings");
  const tok = await page.evaluate(() => localStorage.getItem("continuum.token"));
  while (Math.floor(Date.now() / 30000) <= step) await page.waitForTimeout(500); // step+2 enters the ±1 window
  expect((await request.post("/v1/auth/mfa/disable", { data: { code: totp(secret, step + 2) }, headers: { Authorization: `Bearer ${tok}` } })).status()).toBe(200);
});

test("two-person approval for deleting a user", async ({ page, request }) => {
  const admin = await login(request, "admin", "admin-pass-123");
  const auth = { Authorization: `Bearer ${admin}` };
  expect((await request.post("/v1/users", { data: { username: "sec", password: "security-pass-1", role: "security-admin" }, headers: auth })).status()).toBe(201);
  expect((await request.post("/v1/users", { data: { username: "temp", password: "temporary-pass-1", role: "viewer" }, headers: auth })).status()).toBe(201);

  await page.addInitScript((t) => localStorage.setItem("continuum.token", t), admin);
  await page.goto("/#/settings");
  const ap = page.getByTestId("approvals");
  await ap.getByLabel("user.delete").check();
  await ap.getByRole("button", { name: "Save" }).click();
  await expect(page.getByText("Approval policy saved")).toBeVisible();

  const row = page.getByTestId("users").getByRole("row", { name: /temp/ });
  await row.getByRole("button", { name: "Delete" }).click();
  await expect(page.getByText(/needs a second person's approval/)).toBeVisible();
  await expect(ap).toContainText("user.delete");
  await page.screenshot({ path: join(shots, "03-approval-pending.png"), fullPage: true });
  // the requester cannot approve their own request
  await ap.getByRole("button", { name: "Approve" }).click();
  await expect(page.getByText(/a second person must decide/)).toBeVisible();

  const sec = await login(request, "sec", "security-pass-1");
  const pending = await (await request.get("/v1/approvals?state=pending", { headers: { Authorization: `Bearer ${sec}` } })).json();
  expect(pending).toHaveLength(1);
  const ok = await request.post(`/v1/approvals/${pending[0].approval_id}/approve`, { data: {}, headers: { Authorization: `Bearer ${sec}` } });
  expect(ok.status()).toBe(200);

  await row.getByRole("button", { name: "Delete" }).click(); // the UI retries with X-Approval-Id
  await expect(page.getByText("temp deleted")).toBeVisible();
  await expect(page.getByTestId("users")).not.toContainText("temp");
  await page.screenshot({ path: join(shots, "04-approved-delete.png"), fullPage: true });
});
