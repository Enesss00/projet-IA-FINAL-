import { defineConfig } from "vitest/config";

const backend = process.env.PITWALL_BACKEND ?? "http://127.0.0.1:8080";

export default defineConfig({
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/ws": { target: backend.replace(/^http/, "ws"), ws: true },
      "/healthz": backend,
    },
  },
  build: {
    outDir: "../server/internal/webui/dist",
    emptyOutDir: true,
    target: "es2022",
    sourcemap: false,
    chunkSizeWarningLimit: 600,
  },
  test: {
    environment: "jsdom",
    include: ["tests/**/*.test.ts"],
  },
});
