import { expect, test } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const work = process.env.CONTINUUM_E2E_DIR!;
const shots = resolve(__dirname, "../../../test/impl/14/logs");

// English UI: selectors below use the English labels
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("continuum.prefs", JSON.stringify({ lang: "en", theme: "light", units: "si" })));
});

async function waitJob(request: import("@playwright/test").APIRequestContext, type: string) {
  for (let i = 0; i < 100; i++) {
    const jobs = await (await request.get("/v1/jobs")).json();
    const j = jobs.find((x: { type: string }) => x.type === type);
    if (j && ["DONE", "FAILED", "CANCELLED"].includes(j.state)) return j;
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`${type} did not finish`);
}

// E14: Rebalance after data landed on #3, then Drain #1 from the disk detail page (FR-P10, FR-D10).
test("rebalance and drain with plan preview", async ({ page, request }) => {
  const ids: string[] = [];
  for (const n of ["rb-1", "rb-2", "rb-3"]) {
    const p = join(work, "disks", n);
    mkdirSync(p, { recursive: true });
    ids.push((await (await request.post("/v1/disks", { data: { path: p, name: n, min_free_bytes: 0 } })).json()).disk.disk_id);
  }
  expect((await request.post("/v1/volumes", { data: { name: "Reloc Lab", min_free_bytes: 0, disk_ids: ids } })).status()).toBe(201);
  const src = join(work, "src", "reloc");
  mkdirSync(src, { recursive: true });
  for (let i = 0; i < 5; i++) writeFileSync(join(src, `r${i}.bin`), Buffer.alloc(40_000, i));
  expect((await request.post("/v1/jobs", { data: { volume: "reloc-lab", source: src + "/", destination: "/reloc", forced_ordinal: 3 } })).status()).toBe(202);
  await waitJob(request, "COPY");

  await page.goto("/#/volumes/reloc-lab/disks");
  await page.getByRole("button", { name: "Rebalance…" }).click();
  const dlg = page.getByRole("dialog");
  await expect(dlg.getByTestId("plan-view")).toContainText("5 files");
  await page.screenshot({ path: join(shots, "01-rebalance-plan.png"), fullPage: true });
  await dlg.getByRole("button", { name: "Start rebalance" }).click();
  await expect(page).toHaveURL(/#\/transfers/);
  expect((await waitJob(request, "REBALANCE")).state).toBe("DONE");
  const loc = await (await request.get("/v1/volumes/reloc-lab/locate?path=/reloc/r0.bin")).json();
  expect(loc[0].disk_name).toBe("rb-1");

  await page.goto(`/#/disks/${ids[0]}`);
  await page.getByRole("button", { name: "Drain…" }).click();
  await expect(page.getByRole("dialog").getByTestId("plan-view")).toContainText("5 files");
  await page.screenshot({ path: join(shots, "02-drain-plan.png"), fullPage: true });
  await page.getByRole("dialog").getByRole("button", { name: "Start drain" }).click();
  expect((await waitJob(request, "DRAIN")).state).toBe("DONE");
  const d1 = await (await request.get(`/v1/disks/${ids[0]}`)).json();
  expect(d1.state).toBe("DISABLED");
  expect(d1.volume_id ?? "").toBe("");
  await page.goto("/#/volumes/reloc-lab/disks");
  await expect(page.getByRole("link", { name: "rb-1" })).toHaveCount(0);
  await page.goto("/#/transfers");
  await page.screenshot({ path: join(shots, "03-transfers.png"), fullPage: true });
});
