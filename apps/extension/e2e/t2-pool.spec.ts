// E27 (FR-X02): T2 browser-local pool — OPFS folders stand in for picked folders (native pickers cannot be
// automated); placement, copy, SHA-256 verification, merged listing and persistence run for real.
import { chromium, expect, test } from "@playwright/test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const ext = resolve(here, "../.output/chrome-mv3");
const shots = resolve(here, "../../../test/impl/27/logs");

test("T2: ordered folder pool fills in order and verifies copies", async () => {
  const ctx = await chromium.launchPersistentContext(mkdtempSync(join(tmpdir(), "ext-t2-")), {
    channel: "chromium",
    headless: true,
    args: [`--disable-extensions-except=${ext}`, `--load-extension=${ext}`],
  });
  let [sw] = ctx.serviceWorkers();
  if (!sw) sw = await ctx.waitForEvent("serviceworker", { timeout: 15_000 });
  const id = new URL(sw.url()).host;
  const page = await ctx.newPage();
  await page.setViewportSize({ width: 1100, height: 1000 });
  await page.goto(`chrome-extension://${id}/t2.html?opfs=1`);
  await page.getByTestId("t2-add").click();
  await page.getByTestId("t2-add").click();
  await expect(page.getByTestId("t2-folders").locator("tr")).toHaveCount(2);
  const q1 = page.getByLabel("Quota of folder-1");
  await q1.fill("3 MB");
  await q1.blur();
  await page.getByLabel("Quota of folder-2").fill("100 MB");
  await page.getByLabel("Quota of folder-2").blur();

  const mb = (n: number, b: number) => Buffer.alloc(n * 1_000_000, b);
  await page.getByTestId("t2-files").setInputFiles([
    { name: "a.bin", mimeType: "application/octet-stream", buffer: mb(2, 1) },
    { name: "b.bin", mimeType: "application/octet-stream", buffer: mb(2, 2) }, // folder-1 has 1 MB left → folder-2
    { name: "c.txt", mimeType: "text/plain", buffer: Buffer.from("small") }, // still fits folder-1
  ]);
  await page.getByLabel("Destination").fill("/inbox");
  await page.getByRole("button", { name: "Preview placement" }).click();
  const plan = page.getByTestId("t2-plan");
  await expect(plan).toContainText("folder-1");
  await expect(plan.locator(".row").nth(0)).toContainText("2 files");
  await expect(plan.locator(".row").nth(1)).toContainText("1 files");
  await page.getByRole("button", { name: "Copy", exact: true }).click();
  await expect(page.getByTestId("t2-log")).toContainText("done: 3/3 verified (SHA-256), 0 did not fit");
  await expect(page.getByTestId("t2-listing")).toContainText("inbox");
  await page.screenshot({ path: join(shots, "01-t2-pool.png"), fullPage: true });

  // physically in the expected folders
  const where = await page.evaluate(async () => {
    const root = await navigator.storage.getDirectory();
    const out: Record<string, string[]> = {};
    for (const f of ["folder-1", "folder-2"]) {
      const inbox = await (await root.getDirectoryHandle(f)).getDirectoryHandle("inbox");
      out[f] = [];
      for await (const [n] of inbox as unknown as AsyncIterable<[string]>) out[f].push(n);
      out[f].sort();
    }
    return out;
  });
  expect(where).toEqual({ "folder-1": ["a.bin", "c.txt"], "folder-2": ["b.bin"] });

  // persistence + reorder
  await page.reload();
  await expect(page.getByTestId("t2-folders").locator("tr")).toHaveCount(2);
  await page.getByRole("button", { name: "Move folder-2 up" }).click();
  await expect(page.getByTestId("t2-folders").locator("tr").first()).toContainText("folder-2");
  await ctx.close();
});
