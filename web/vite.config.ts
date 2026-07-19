import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const apiTarget =
    loadEnv(mode, ".", "OXBIN_").OXBIN_API_PROXY_TARGET ??
    "http://127.0.0.1:8080";
  return {
    plugins: [react()],
    build: {
      outDir: "../internal/webassets/dist",
      emptyOutDir: false,
    },
    server: {
      proxy: {
        "/api": apiTarget,
        "/health": apiTarget,
      },
    },
  };
});
