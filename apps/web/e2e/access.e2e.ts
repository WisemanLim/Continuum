import { expect, test } from "@playwright/test";
import { execSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const work = process.env.CONTINUUM_E2E_DIR!;
const shots = resolve(__dirname, "../../../test/impl/15/logs");

// English UI: selectors below use the English labels
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("continuum.prefs", JSON.stringify({ lang: "en", theme: "light", units: "si" })));
});

function hasImage() {
  try {
    execSync("docker image inspect continuum-samba:local", { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

// E15: Access tab — enable the SMB container, switch direct-access mode, disable (FR-A01·A08·A09).
test("access point: enable, mode switch, status on volume card", async ({ page, request }) => {
  test.skip(!hasImage(), "Docker image continuum-samba:local not built (make local-build)");
  const ids: string[] = [];
  for (const n of ["ac-1", "ac-2"]) {
    const p = join(work, "disks", n);
    mkdirSync(p, { recursive: true });
    ids.push((await (await request.post("/v1/disks", { data: { path: p, name: n, min_free_bytes: 0 } })).json()).disk.disk_id);
  }
  expect((await request.post("/v1/volumes", { data: { name: "Share Lab", min_free_bytes: 0, disk_ids: ids } })).status()).toBe(201);

  await page.goto("/#/volumes/share-lab/access");
  await page.getByRole("button", { name: "Enable access point" }).click();
  await expect(page.getByText(/running · \d+ clients/)).toBeVisible({ timeout: 30_000 });
  const shares = page.getByTestId("shares");
  await expect(shares.getByRole("row", { name: /share-lab whole volume/ })).toContainText("read-only");
  await expect(shares).toContainText("smb://127.0.0.1:4445/share-lab");
  await expect(shares.locator("tbody tr")).toHaveCount(3);

  await page.getByLabel(/Full direct access/).click();
  await expect(page.getByLabel(/Full direct access/)).toBeChecked();
  await expect(shares.getByRole("row", { name: /share-lab whole volume/ })).toContainText("read-write", { timeout: 30_000 });
  await expect(shares).toContainText("1:ac-1=RW · 2:ac-2=RW");
  await page.getByRole("button", { name: "Show credentials" }).click();
  await expect(page.getByTestId("access-credentials")).toContainText("user continuum · password ");
  await page.screenshot({ path: join(shots, "01-access-tab.png"), fullPage: true, mask: [page.getByTestId("access-credentials")] });

  await page.goto("/#/volumes");
  await expect(page.getByTestId("access-share-lab")).toContainText("smb://127.0.0.1:4445/share-lab · mounted");
  await page.screenshot({ path: join(shots, "02-volume-card.png"), fullPage: true });

  await page.goto("/#/volumes/share-lab/access");
  await page.getByRole("button", { name: "Disable" }).click();
  await expect(page.getByRole("button", { name: "Enable access point" })).toBeVisible({ timeout: 30_000 });
  expect(execSync("docker ps -a --filter name=continuum-smb -q").toString().trim()).toBe("");
});
