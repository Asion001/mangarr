import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import type { Plugin } from "vite";

/** offlineManifest lists the built app's files for the service worker (public/sw.js) to keep, so the app opens offline. */
function offlineManifest(): Plugin {
  return {
    name: "mangarr-offline-manifest",
    apply: "build",
    generateBundle(_, bundle) {
      const files = Object.keys(bundle).filter((f) => f.startsWith("assets/") && !f.endsWith(".map")).sort();
      this.emitFile({ type: "asset", fileName: "offline-manifest.json", source: JSON.stringify(files) });
    },
  };
}

// Assets are referenced relatively so the UI works under a URL base.
export default defineConfig({
  base: "./",
  plugins: [react(), tailwindcss(), offlineManifest()],
  build: { outDir: "dist", emptyOutDir: true, chunkSizeWarningLimit: 1500 },
  server: {
    port: 5173,
    proxy: { "/api": { target: "http://localhost:8787", changeOrigin: false } },
  },
});
