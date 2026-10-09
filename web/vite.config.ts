import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { defineConfig } from "vitest/config";

const outDir = "../server/internal/webui/dist";

// go:embed needs the directory to exist in a fresh clone: keep a committed
// placeholder that `emptyOutDir` would otherwise delete.
const keepEmbedDir = {
  name: "pitwall-keep-embed-dir",
  closeBundle() {
    writeFileSync(resolve(import.meta.dirname, outDir, ".gitkeep"), "");
  },
};

const backend = process.env.PITWALL_BACKEND ?? "http://127.0.0.1:8080";

export default defineConfig({
  plugins: [keepEmbedDir],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/ws": { target: backend.replace(/^http/, "ws"), ws: true },
      "/healthz": backend,
    },
  },
  build: {
    outDir,
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
