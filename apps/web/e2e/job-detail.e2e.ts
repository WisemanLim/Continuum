import { expect, test } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const work = process.env.CONTINUUM_E2E_DIR!;
const shots = resolve(__dirname, "../../../test/impl/17/logs");

// E17: folder picker (copy source) → job detail page with items; language switch ko/en (CTM-TRF-020, FR-G13).
test("folder picker, job detail, ko/en switch", async ({ page, request }) => {
  await page.addInitScript(() => {
    if (!sessionStorage.getItem("prefs-set")) {
      localStorage.setItem("continuum.prefs", JSON.stringify({ lang: "en", theme: "light", units: "si" }));
      sessionStorage.setItem("prefs-set", "1");
    }
  });
  const p = join(work, "disks", "jd-1");
  mkdirSync(p, { recursive: true });
  const disk = (await (await request.post("/v1/disks", { data: { path: p, name: "jd-1", min_free_bytes: 0 } })).json()).disk;
  expect((await request.post("/v1/volumes", { data: { name: "Job Lab", min_free_bytes: 0, disk_ids: [disk.disk_id] } })).status()).toBe(201);
  const src = join(work, "src", "picker-src", "photos");
  mkdirSync(src, { recursive: true });
  for (let i = 0; i < 6; i++) writeFileSync(join(src, `p${i}.jpg`), Buffer.alloc(10_000, i));

  await page.goto("/#/transfers");
  await page.getByTestId("new-copy").click();
  await page.getByTestId("copy-source").fill(join(work, "src", "picker-src"));
  await page.getByRole("button", { name: "Browse…" }).click();
  await expect(page.getByRole("dialog", { name: "Choose a folder" })).toBeVisible();
  await expect(page.getByTestId("pick-path")).toHaveText(join(work, "src", "picker-src"));
  await page.getByRole("option", { name: /photos/ }).click();
  await expect(page.getByTestId("pick-path")).toHaveText(new RegExp("photos$"));
  await expect(page.getByText("6 files in this folder")).toBeVisible();
  await page.screenshot({ path: join(shots, "01-folder-picker.png"), fullPage: true });
  await page.getByTestId("pick-choose").click();
  await expect(page.getByTestId("copy-source")).toHaveValue(new RegExp("photos$"));

  const r = await request.post("/v1/jobs", { data: { volume: "job-lab", source: src, destination: "/photos" } });
  const job = await r.json();
  await page.keyboard.press("Escape");
  await page.goto(`/#/transfers/${job.job_id}`);
  await expect(page.getByText("DONE").first()).toBeVisible({ timeout: 20_000 });
  await expect(page.getByTestId("job-items").locator("tbody tr")).toHaveCount(6);
  await page.getByLabel("Filter").selectOption("failed");
  await expect(page.getByText("No items")).toBeVisible();
  await page.screenshot({ path: join(shots, "02-job-detail.png"), fullPage: true });

  // language switch: Settings → 한국어 → labels change everywhere
  await page.goto("/#/settings");
  await page.getByLabel("Language").selectOption("ko");
  await expect(page.getByRole("heading", { name: "설정" })).toBeVisible();
  await page.goto(`/#/transfers/${job.job_id}`);
  await expect(page.getByRole("heading", { name: "항목" })).toBeVisible();
  await page.screenshot({ path: join(shots, "03-job-detail-ko.png"), fullPage: true });
});
