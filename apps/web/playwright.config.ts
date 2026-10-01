// E2E: real Chrome (channel) against a real Controller that serves the built UI (same origin).
// Run: pnpm --filter @continuum/web build && pnpm --filter @continuum/web e2e
import { defineConfig } from "@playwright/test";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));

const work = process.env.CONTINUUM_E2E_DIR ?? mkdtempSync(join(tmpdir(), "continuum-e2e-"));
process.env.CONTINUUM_E2E_DIR = work;
const port = 18090;

export default defineConfig({
  testDir: "./e2e",
  testMatch: /.*\.e2e\.ts/,
  timeout: 90_000,
  fullyParallel: false,
  workers: 1,
  reporter: [["list"]],
  outputDir: resolve(__dirname, "../../test/impl/8/logs/playwright"),
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    channel: "chrome",
    headless: true,
    viewport: { width: 1600, height: 1000 },
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  webServer: {
    command: `go run ../../cmd/controller`,
    url: `http://127.0.0.1:${port}/healthz`,
    reuseExistingServer: false,
    timeout: 120_000,
    env: {
      ENV: "e2e",
      CONTROLLER_ADDR: `127.0.0.1:${port}`,
      DATABASE_URL: `sqlite:///${join(work, "e2e.db")}`,
      CONTINUUM_WEB_DIR: resolve(__dirname, "dist"),
      CONTINUUM_ACCESS_DIR: join(work, "access"),
      CONTINUUM_AGENT_ADDR: "127.0.0.1:18444",
      CONTINUUM_AGENT_URL: "https://127.0.0.1:18444",
      CONTINUUM_AGENT_HEARTBEAT_SEC: "1",
      CONTINUUM_PKI_DIR: join(work, "pki"),
      CONTINUUM_SECRETS: "file", // never touch the developer's login keychain from tests
      CONTINUUM_SECRETS_DIR: join(work, "secrets"),
      CONTINUUM_WATCH: "off", // specs assert exact SCAN reports; the watcher is covered by Go tests
    },
  },
});
