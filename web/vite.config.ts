import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "OXBIN_");
  const apiTarget = env.OXBIN_API_PROXY_TARGET ?? "http://127.0.0.1:8080";
  const hostedPreview = env.OXBIN_HOSTED_PREVIEW === "true";
  return {
    plugins: [
      react(),
      {
        name: "0xbin-hosted-preview",
        transformIndexHtml(html: string) {
          return hostedPreview
            ? html.replace(
                'data-hosted-service="false"',
                'data-hosted-service="true"',
              )
            : html;
        },
      },
    ],
    build: {
      outDir: "../internal/webassets/dist",
      emptyOutDir: true,
    },
    server: {
      proxy: {
        "/api": apiTarget,
        "/health": apiTarget,
      },
    },
  };
});
