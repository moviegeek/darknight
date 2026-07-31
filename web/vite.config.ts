import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Dev: Vite runs on :5173 and proxies /api to the Go server on :8080.
// Prod: `npm run build` emits to web/dist, which the Go binary embeds and
// serves at the same origin — no proxy needed.
export default defineConfig({
  plugins: [react()],
  server: {
    host: "0.0.0.0", // LAN access: reachable from other devices on the network
    port: 5173,
    proxy: {
      "/api": "http://localhost:8080",
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
