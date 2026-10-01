import { expect, test } from "@playwright/test";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const work = process.env.CONTINUUM_E2E_DIR!;
const shots = resolve(__dirname, "../../../test/impl/13/logs");

// English UI: selectors below use the English labels
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("continuum.prefs", JSON.stringify({ lang: "en", theme: "light", units: "si" })));
});

// E13: disk detail — SMART, benchmark, usage treemap, duplicates → trash (CTM-DSK-020~023).
test("disk detail: SMART, benchmark, usage, duplicates", async ({ page, request }) => {
  const p = join(work, "disks", "dd-1");
  mkdirSync(p, { recursive: true });
  const r = await request.post("/v1/disks", { data: { path: p, name: "dd-1", min_free_bytes: 0 } });
  const { disk } = await r.json();
  expect((await request.post("/v1/volumes", { data: { name: "Detail Lab", min_free_bytes: 0, disk_ids: [disk.disk_id] } })).status()).toBe(201);
  const blob = Buffer.alloc(200_000, 7);
  mkdirSync(join(disk.data_root, "photos"), { recursive: true });
  mkdirSync(join(disk.data_root, "backup"), { recursive: true });
  writeFileSync(join(disk.data_root, "photos", "a.raw"), blob);
  writeFileSync(join(disk.data_root, "backup", "a (copy).raw"), blob);
  writeFileSync(join(disk.data_root, "readme.md"), "hi");
  expect((await request.post("/v1/volumes/detail-lab/scan", { data: {} })).status()).toBe(200);

  await page.goto("/#/nodes");
  await page.getByRole("link", { name: "dd-1" }).click();
  await expect(page).toHaveURL(/#\/disks\/dsk_/);
  await expect(page.getByTestId("smart-status")).toBeVisible();
  await expect(page.getByRole("cell", { name: disk.data_root })).toBeVisible();
  await page.screenshot({ path: join(shots, "01-overview-smart.png"), fullPage: true });

  await page.getByRole("tab", { name: "Benchmark" }).click();
  await page.getByLabel("Test file size").selectOption("64");
  await page.getByRole("button", { name: "Run benchmark" }).click();
  await expect(page.getByTestId("bench-table").locator("tbody tr")).toHaveCount(1, { timeout: 30_000 });
  await expect(page.getByTestId("bench-table")).toContainText("MB/s");
  await page.screenshot({ path: join(shots, "02-benchmark.png"), fullPage: true });

  await page.getByRole("tab", { name: "Usage" }).click();
  await expect(page.getByRole("list", { name: "Folder sizes" }).getByRole("listitem")).toHaveCount(2);
  await expect(page.getByRole("cell", { name: ".raw", exact: true })).toBeVisible();
  await page.screenshot({ path: join(shots, "03-usage.png"), fullPage: true });

  await page.getByRole("tab", { name: "Duplicates" }).click();
  await page.getByRole("button", { name: "Find duplicates" }).click();
  await expect(page.getByTestId("dupes")).toContainText("1 groups");
  await page.screenshot({ path: join(shots, "04-duplicates.png"), fullPage: true });
  await page.getByRole("button", { name: "Move to trash" }).click();
  await expect(page.getByTestId("dupes")).toContainText("0 groups");
  const trash = await (await request.get("/v1/volumes/detail-lab/trash")).json();
  expect(trash).toHaveLength(1);
});

// E25: wipe a disk outside any volume from its detail page (name typed to confirm).
test("wipe an unassigned disk", async ({ page, request }) => {
  const p = join(work, "disks", "wipe-me");
  mkdirSync(p, { recursive: true });
  const { disk } = await (await request.post("/v1/disks", { data: { path: p, name: "wipe-me", min_free_bytes: 0 } })).json();
  writeFileSync(join(disk.data_root, "leftover.bin"), Buffer.alloc(50_000, 1));
  await page.goto(`/#/disks/${disk.disk_id}`);
  await page.getByRole("button", { name: "Wipe…" }).click();
  await page.getByRole("dialog").getByRole("textbox").fill("wipe-me");
  await page.getByRole("dialog").getByRole("button", { name: /Wipe/ }).click();
  await expect(page).toHaveURL(/#\/nodes/);
  expect((await request.get(`/v1/disks/${disk.disk_id}`)).status()).toBe(404);
  expect(existsSync(disk.data_root)).toBe(false);
});
