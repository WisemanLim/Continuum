import { defineConfig } from "wxt";

// MV3, 최소 권한 (FR-X05). Controller origin 은 런타임 optional_host_permissions 로 요청.
// nativeMessaging (T3) 은 optional — 사용자가 "Connect local agent" 를 누를 때 요청한다.
// CONTINUUM_EXT_NATIVE=required: E2E 용 빌드(.output-t3)는 권한 대화상자를 자동화할 수 없어 필수 권한으로 둔다.
const env = (globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env ?? {};
const nativeRequired = env.CONTINUUM_EXT_NATIVE === "required";

export default defineConfig({
  outDir: nativeRequired ? ".output-t3" : ".output",
  modules: ["@wxt-dev/module-react"],
  // the full console reuses apps/web sources (React UI shared with the desktop shell)
  vite: () => ({ server: { fs: { allow: ["../.."] } }, resolve: { dedupe: ["react", "react-dom"] } }),
  manifest: {
    name: "Continuum",
    description: "Continuum console (T1 remote, T3 with the local agent) — storage pooling by ordinal",
    permissions: nativeRequired ? ["storage", "sidePanel", "nativeMessaging"] : ["storage", "sidePanel"],
    optional_permissions: nativeRequired ? [] : ["nativeMessaging"],
    optional_host_permissions: ["https://*/*", "http://localhost/*"],
  },
});
