import { expect, test } from "@playwright/test";
import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const work = process.env.CONTINUUM_E2E_DIR!;
const shots = resolve(__dirname, "../../../test/impl/19/logs");

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("continuum.prefs", JSON.stringify({ lang: "en", theme: "light", units: "si" })));
});

// E18/E19 UI: enrollment token → real agent process enrolls and connects → attach a folder on that node → revoke.
test("enroll an agent, attach its folder, revoke", async ({ page, request }) => {
  const bin = join(work, "continuum-agent");
  execFileSync("go", ["build", "-o", bin, "./cmd/agent"], { cwd: resolve(__dirname, "../../.."), stdio: "inherit" });
  const root = join(work, "agent-root");
  mkdirSync(root, { recursive: true });

  await page.goto("/#/nodes");
  await page.getByTestId("token-open").click();
  const cmd = await page.getByTestId("token-command").textContent();
  const token = /--token (CTM1\.\S+)/.exec(cmd ?? "")![1];
  await page.screenshot({ path: join(shots, "01-token.png"), fullPage: true, mask: [page.getByTestId("token-command")] });
  await page.getByRole("button", { name: "Close" }).click();

  let agent: ChildProcess | undefined;
  try {
    agent = spawn(bin, ["run", "--dir", join(work, "agent-state"), "--controller", "https://127.0.0.1:18444", "--token", token, "--root", root], { stdio: "inherit" });
    await expect.poll(async () => (await (await request.get("/v1/nodes")).json()).some((n: { remote: boolean; state: string }) => n.remote && n.state === "ONLINE"), { timeout: 20_000 }).toBe(true);
    await page.reload();
    await expect(page.getByText("remote agent").first()).toBeVisible();

    await page.getByTestId("attach-open").click();
    const nodes = await (await request.get("/v1/nodes")).json();
    const remote = nodes.find((n: { remote: boolean; state: string }) => n.remote && n.state === "ONLINE");
    await page.getByTestId("attach-node").selectOption(remote.node_id);
    await expect(page.getByRole("cell", { name: root })).toBeVisible({ timeout: 10_000 });
    await page.getByRole("row", { name: new RegExp(root) }).getByRole("button", { name: "Use" }).click();
    await page.getByTestId("attach-minfree").fill("0");
    await page.getByRole("button", { name: "Next: preflight" }).click();
    await expect(page.getByTestId("preflight").locator("li")).toHaveCount(10);
    await page.getByTestId("attach-confirm").click();
    await expect.poll(async () => (await (await request.get("/v1/disks")).json()).some((d: { node_id: string }) => d.node_id === remote.node_id)).toBe(true);
    await page.screenshot({ path: join(shots, "02-remote-disk.png"), fullPage: true });

    await page.getByRole("button", { name: "Revoke" }).first().click();
    await page.getByRole("dialog").getByRole("textbox").fill(remote.hostname);
    await page.getByRole("dialog").getByRole("button", { name: /Revoke/ }).click();
    await expect.poll(async () => (await (await request.get("/v1/nodes")).json()).find((n: { node_id: string }) => n.node_id === remote.node_id).state).toBe("REVOKED");
  } finally {
    agent?.kill();
  }
});
