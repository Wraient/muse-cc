import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The panel is served from the Go binary at "/", so the built assets must use
// relative paths. In dev, /api requests proxy to the running Go server on :8788.
export default defineConfig({
  plugins: [react()],
  base: "./",
  server: {
    port: 5174,
    proxy: {
      "/api": "http://127.0.0.1:8788",
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    chunkSizeWarningLimit: 1200,
  },
});
