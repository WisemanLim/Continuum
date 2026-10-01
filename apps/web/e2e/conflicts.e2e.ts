import { expect, test } from "@playwright/test";
import { mkdirSync, utimesSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const work = process.env.CONTINUUM_E2E_DIR!;
const shots = resolve(__dirname, "../../../test/impl/12/logs");

// English UI: selectors below use the English labels
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("continuum.prefs", JSON.stringify({ lang: "en", theme: "light", units: "si" })));
});

// E11/E12: files edited directly on the disks → Scan now → Conflict Center resolve (CTM-VOL-035).
test("scan picks up direct edits and Conflict Center resolves copies", async ({ page, request }) => {
  const roots: string[] = [];
  const ids: string[] = [];
  for (const n of ["cf-a", "cf-b"]) {
    const p = join(work, "disks", n);
    mkdirSync(p, { recursive: true });
    const r = await request.post("/v1/disks", { data: { path: p, name: n, min_free_bytes: 0 } });
    expect(r.status()).toBe(201);
    const j = await r.json();
    ids.push(j.disk.disk_id);
    roots.push(j.disk.data_root);
  }
  expect((await request.post("/v1/volumes", { data: { name: "Conflict Lab", min_free_bytes: 0, disk_ids: ids } })).status()).toBe(201);
  writeFileSync(join(roots[0], "same.txt"), "identical");
  writeFileSync(join(roots[1], "same.txt"), "identical");
  writeFileSync(join(roots[0], "report.docx"), "old");
  writeFileSync(join(roots[1], "report.docx"), "newer version");
  const later = new Date(Date.now() + 3600_000);
  utimesSync(join(roots[1], "report.docx"), later, later);

  await page.goto("/#/volumes/conflict-lab/conflicts");
  await expect(page.getByText("No conflicts")).toBeVisible();
  await page.getByRole("button", { name: "Scan now" }).click();
  await expect(page.getByTestId("scan-report")).toContainText("added 4");
  await expect(page.getByTestId("scan-report")).toContainText("conflicts 2");
  const row = page.getByRole("row", { name: /report\.docx/ });
  await expect(row).toContainText("conflict");
  await expect(page.getByRole("row", { name: /same\.txt/ })).toContainText("duplicate");
  await page.screenshot({ path: join(shots, "01-conflicts.png"), fullPage: true });

  await row.getByRole("button", { name: "Keep newest" }).click();
  await expect(page.getByRole("row", { name: /report\.docx/ })).toHaveCount(0);
  await page.getByRole("button", { name: "Resolve 1 duplicates" }).click();
  await expect(page.getByText("No conflicts")).toBeVisible();
  await page.screenshot({ path: join(shots, "02-resolved.png"), fullPage: true });

  await page.getByRole("tab", { name: "Trash" }).click();
  await expect(page.getByRole("row", { name: /report\.docx/ })).toBeVisible();
  const loc = await (await request.get("/v1/volumes/conflict-lab/locate?path=/report.docx")).json();
  expect(loc).toHaveLength(1);
  expect(loc[0].disk_name).toBe("cf-b");
});
