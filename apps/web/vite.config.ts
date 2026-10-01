import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 정적 SPA — Tauri(desktop) 와 wxt(extension) 가 같은 빌드를 재사용한다.
export default defineConfig({
  plugins: [react()],
  envDir: "../..", // 루트 .env.* 의 VITE_* 사용
  server: { port: 5173, strictPort: true },
});
