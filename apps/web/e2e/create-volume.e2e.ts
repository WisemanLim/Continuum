import { expect, test, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));

const work = process.env.CONTINUUM_E2E_DIR!;
const shots = resolve(__dirname, "../../../test/impl/8/logs");
const disk = (n: string) => {
  const p = join(work, "disks", n);
  mkdirSync(p, { recursive: true });
  return p;
};

async function shot(page: Page, name: string) {
  await page.screenshot({ path: join(shots, `${name}.png`), fullPage: true });
}

test("attach → create volume (drag + keyboard DnD) → copy with plan → locate", async ({ page, request }) => {
  // dataset: 3 folders × 4 files
  const src = join(work, "src", "research-data");
  for (let f = 0; f < 3; f++)
    for (let i = 0; i < 4; i++) {
      mkdirSync(join(src, `folder0${f}`), { recursive: true });
      writeFileSync(join(src, `folder0${f}`, `file${i}.bin`), Buffer.alloc(50_000, f * 10 + i));
    }
  const d1 = disk("disk-01"),
    d2 = disk("win-d"),
    d3 = disk("mac-ext");

  // 1) Storage Nodes › Attach storage wizard (CTM-DSK-010/011) for disk-01
  await page.goto("/#/nodes");
  await expect(page.getByTestId("agent-status")).toContainText("모든 Agent 온라인");
  await page.getByTestId("attach-open").click();
  await page.getByTestId("attach-path").fill(d1);
  await page.getByTestId("attach-minfree").fill("0");
  await page.getByRole("button", { name: "다음: 사전 점검" }).click();
  await expect(page.getByTestId("preflight").locator("li")).toHaveCount(10);
  await shot(page, "01-attach-preflight");
  await page.getByTestId("attach-confirm").click();
  await expect(page.getByRole("cell", { name: /disk-01/ }).first()).toBeVisible();
  // other two via API (same endpoint the wizard uses)
  for (const [p, role] of [
    [d2, "always-on"],
    [d3, "portable"],
  ])
    expect((await request.post("/v1/disks", { data: { path: p, role, min_free_bytes: 0 } })).status()).toBe(201);
  await page.reload();
  await shot(page, "02-nodes");

  // 2) Create Volume (CTM-VOL-020): pointer drag disk-01 into the drop zone
  await page.goto("/#/volumes/new");
  await expect(page.getByRole("heading", { name: "사용 가능한 스토리지" })).toBeVisible();
  const handle = page.getByRole("button", { name: "Drag disk-01" });
  const zone = page.getByTestId("dropzone").first();
  const hb = (await handle.boundingBox())!;
  const zb = (await zone.boundingBox())!;
  await page.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2);
  await page.mouse.down();
  await page.mouse.move(hb.x + 30, hb.y + 30, { steps: 5 }); // > 5 px activation
  await page.mouse.move(zb.x + zb.width / 2, zb.y + zb.height / 2, { steps: 15 });
  await page.mouse.up();
  await expect(page.getByTestId("order-1")).toContainText("disk-01");
  await page.waitForTimeout(300); // dnd-kit restores focus to the drag activator after drop (a11y)

  // add win-d and mac-ext with the "+" buttons (keyboard-accessible path)
  await page.getByRole("button", { name: "Add win-d to volume" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByTestId("order-2")).toContainText("win-d");
  await page.getByRole("button", { name: "Add mac-ext to volume" }).click();
  await expect(page.getByTestId("order-3")).toContainText("mac-ext");

  // keyboard reorder: move mac-ext up with the ↑ button, then back down
  await page.getByRole("button", { name: "Move mac-ext up" }).click();
  await expect(page.getByTestId("order-2")).toContainText("mac-ext");
  await page.getByRole("button", { name: "Move mac-ext down" }).click();
  await expect(page.getByTestId("order-3")).toContainText("mac-ext");
  await expect(page.getByTestId("summary")).toContainText("3 디스크");

  // configuration form
  await page.getByLabel("볼륨 이름").fill("Research Pool");
  await page.getByLabel(/최소 여유 공간/).fill("1 MB");
  await shot(page, "03-create-volume");
  await page.getByRole("button", { name: "볼륨 저장" }).first().click(); // → step 3 Review & save
  await expect(page.getByRole("heading", { name: "검토·저장" })).toBeVisible();
  await page.getByTestId("save-volume").click();
  // portable warning (CTM-VOL-P01)
  await expect(page.getByRole("dialog")).toContainText("Portable");
  await page.getByRole("button", { name: /그래도 저장/ }).click();
  await expect(page).toHaveURL(/#\/volumes\/research-pool/);
  await expect(page.getByText("디스크 · ").first()).toBeVisible();
  await shot(page, "04-volume-disks");

  // 3) Transfers › New copy › Preview Plan › Copy (CTM-TRF-P01/P02)
  await page.goto("/#/transfers");
  await page.getByTestId("new-copy").click();
  await page.getByTestId("copy-source").fill(src);
  await page.getByLabel("대상 볼륨").selectOption("research-pool"); // other specs may have created volumes first
  await page.getByTestId("copy-dest").fill("/research/2026-q3");
  await page.getByTestId("preview-plan").click();
  const plan = page.getByTestId("plan-view");
  await expect(plan).toContainText("파일 12개");
  await expect(plan).toContainText("제외: role=portable");
  await shot(page, "05-preview-plan");
  await page.getByTestId("start-copy").click();
  await page.getByRole("tab", { name: /완료/ }).click({ timeout: 30_000 }).catch(() => {});
  await expect.poll(async () => (await (await request.get("/v1/jobs")).json())[0]?.state, { timeout: 30_000 }).toBe("DONE");
  await page.reload();
  await page.getByRole("tab", { name: /완료/ }).click();
  await expect(page.getByText("DONE").first()).toBeVisible();
  await shot(page, "06-transfers-done");

  // 4) Volume › Files › Show physical location (FR-K02)
  await page.goto("/#/volumes/research-pool/files");
  await page.getByRole("link", { name: "📁 research", exact: true }).click();
  await page.getByRole("link", { name: "📁 2026-q3", exact: true }).click();
  await page.getByRole("link", { name: "📁 folder00", exact: true }).click();
  await page.getByRole("button", { name: "물리 위치 보기" }).first().click();
  await expect(page.getByRole("dialog")).toContainText(".continuum");
  await shot(page, "07-physical-location");

  // 5) Overview tiles reflect state
  await page.goto("/#/overview");
  await expect(page.getByTestId("tile-볼륨")).toHaveText(/^[1-9]\d*$/); // other specs share this Controller
  await shot(page, "08-overview");

  // 6) Accessibility: dnd-kit keyboard sensor (Space pick · ↓ · Space drop) on the Disks board
  await page.goto("/#/volumes/research-pool/disks");
  const h = page.getByRole("button", { name: "Reorder disk-01" });
  await h.focus();
  await page.keyboard.press("Space"); // pick up
  await page.waitForTimeout(200);
  await page.keyboard.press("ArrowDown"); // move one slot
  await page.waitForTimeout(200);
  await page.keyboard.press("Space"); // drop
  await page.waitForTimeout(200);
  await expect(page.getByRole("button", { name: "순서 적용" })).toBeEnabled();
  await page.getByRole("button", { name: "순서 적용" }).click();
  await expect(page.getByText(/rev 2|rev 3/)).toBeVisible();
});

test("dark theme tokens and ko/en switch", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/#/overview");
  const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
  expect(bg).toBe("rgb(15, 18, 32)"); // --color-bg dark #0F1220
  await page.goto("/#/settings");
  await page.getByLabel("언어").selectOption("en");
  await expect(page.getByRole("link", { name: "Storage Nodes" })).toBeVisible();
  await shot(page, "09-dark-en");
});
