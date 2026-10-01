// E24 (FR-X03): T3 native bridge — Chromium launches `continuum-agent` as a native messaging host.
// Uses the .output-t3 build (nativeMessaging granted at install: permission prompts cannot be automated).
import { chromium, expect, test } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const ext = realpathSync(resolve(here, "../.output-t3/chrome-mv3"));
const shots = resolve(here, "../../../test/impl/24/logs");

// Chromium derives an unpacked extension's id from its absolute path.
function unpackedId(p: string) {
  const h = createHash("sha256").update(p).digest("hex").slice(0, 32);
  return [...h].map((c) => String.fromCharCode("a".charCodeAt(0) + parseInt(c, 16))).join("");
}

test("T3: side panel talks to the local agent through native messaging", async () => {
  const profile = mkdtempSync(join(tmpdir(), "ext-t3-"));
  const bin = join(profile, "continuum-agent");
  execFileSync("go", ["build", "-o", bin, "./cmd/agent"], { cwd: resolve(here, "../../.."), stdio: "inherit" });
  const id = unpackedId(ext);
  // per-profile host manifest (Chromium also reads <user-data-dir>/NativeMessagingHosts)
  execFileSync(bin, ["native-host", "install", "--browser", "chromium", "--extension-id", id, "--manifest-dir", join(profile, "NativeMessagingHosts")], { stdio: "inherit" });

  const ctx = await chromium.launchPersistentContext(profile, {
    channel: "chromium",
    headless: true,
    args: [`--disable-extensions-except=${ext}`, `--load-extension=${ext}`],
  });
  let [sw] = ctx.serviceWorkers();
  if (!sw) sw = await ctx.waitForEvent("serviceworker", { timeout: 15_000 });
  expect(new URL(sw.url()).host).toBe(id);
  const panel = await ctx.newPage();
  await panel.setViewportSize({ width: 400, height: 900 });
  await panel.goto(`chrome-extension://${id}/sidepanel.html`);
  await expect(panel.getByTestId("mode")).toHaveText("T3 native bridge", { timeout: 15_000 });
  await expect(panel.getByTestId("native")).toContainText("Local agent");
  await expect(panel.getByTestId("native")).toContainText("not enrolled (single machine)");
  await expect(panel.getByTestId("native").locator(".mono").first()).toBeVisible(); // local volumes listed
  await expect(panel.getByText("Native bridge to local Agent (T3)").locator("..")).not.toContainText("Not available");
  await panel.screenshot({ path: join(shots, "t3-sidepanel.png") });
  await ctx.close();
});

test("T3 downgrade: without the host the panel stays T1", async () => {
  const profile = mkdtempSync(join(tmpdir(), "ext-t3-none-"));
  mkdirSync(join(profile, "NativeMessagingHosts"), { recursive: true });
  writeFileSync(join(profile, "NativeMessagingHosts", "README"), "no host installed");
  const ctx = await chromium.launchPersistentContext(profile, {
    channel: "chromium",
    headless: true,
    args: [`--disable-extensions-except=${ext}`, `--load-extension=${ext}`],
  });
  let [sw] = ctx.serviceWorkers();
  if (!sw) sw = await ctx.waitForEvent("serviceworker", { timeout: 15_000 });
  const panel = await ctx.newPage();
  await panel.goto(`chrome-extension://${new URL(sw.url()).host}/sidepanel.html`);
  await expect(panel.getByTestId("connect-native")).toBeVisible();
  await expect(panel.getByTestId("mode")).toHaveText("T1 remote console");
  await ctx.close();
});
