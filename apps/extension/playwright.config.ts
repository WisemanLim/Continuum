// Extension E2E: load .output/chrome-mv3 into Chromium and talk to a real Controller (T1 remote console).
import { defineConfig } from "@playwright/test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const work = mkdtempSync(join(tmpdir(), "continuum-ext-"));

export default defineConfig({
  testDir: "./e2e",
  timeout: 60_000,
  workers: 1,
  reporter: [["list"]],
  outputDir: "../../test/impl/9/logs/playwright",
  webServer: {
    command: "go run ../../cmd/controller",
    url: "http://127.0.0.1:18091/healthz",
    timeout: 120_000,
    env: { ENV: "ext-e2e", CONTINUUM_SECRETS: "file", CONTINUUM_SECRETS_DIR: join(work, "secrets"), CONTROLLER_ADDR: "127.0.0.1:18091", DATABASE_URL: `sqlite:///${join(work, "ext.db")}` },
  },
});
