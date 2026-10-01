import { chromium, expect, test } from "@playwright/test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const ext = resolve(here, "../.output/chrome-mv3");
const shots = resolve(here, "../../../test/impl/9/logs");
const CTL = "http://127.0.0.1:18091";

test("T1 side panel connects to the Controller; full console reuses the desktop UI", async () => {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(join(tmpdir(), "ext-profile-")), {
    channel: process.env.PW_CHANNEL || "chromium",
    headless: true,
    args: [`--disable-extensions-except=${ext}`, `--load-extension=${ext}`],
  });
  let [sw] = ctx.serviceWorkers();
  if (!sw) sw = await ctx.waitForEvent("serviceworker", { timeout: 15_000 });
  const id = new URL(sw.url()).host;

  // CTM-EXT-020 side panel
  const panel = await ctx.newPage();
  await panel.setViewportSize({ width: 400, height: 900 });
  await panel.goto(`chrome-extension://${id}/sidepanel.html`);
  await expect(panel.getByTestId("mode")).toHaveText("T1 remote console");
  await panel.getByLabel("Controller URL").fill(CTL);
  await panel.getByRole("button", { name: "Connect", exact: true }).click();
  await expect(panel.getByTestId("conn")).toContainText("Connected");
  await expect(panel.getByTestId("volumes")).toHaveText("0");
  await expect(panel.getByText("Not available in browser mode").first()).toBeVisible(); // FR-X04
  await panel.screenshot({ path: join(shots, "ext-sidepanel.png") });

  // CTM-EXT-030 full console = same React app, pointed at the configured Controller
  const consolePage = await ctx.newPage();
  await consolePage.setViewportSize({ width: 1600, height: 900 });
  await consolePage.goto(`chrome-extension://${id}/console.html#/nodes`);
  await expect(consolePage.getByTestId("agent-status")).toContainText("모든 Agent 온라인");
  await expect(consolePage.getByRole("heading", { name: "스토리지 노드" })).toBeVisible();
  await consolePage.screenshot({ path: join(shots, "ext-console.png") });

  // manifest permissions stay minimal (FR-X05)
  const manifest = await (await consolePage.request.get(`chrome-extension://${id}/manifest.json`).catch(() => null))?.json().catch(() => null);
  if (manifest) expect(manifest.permissions).toEqual(["storage", "sidePanel"]);
  await ctx.close();
});
