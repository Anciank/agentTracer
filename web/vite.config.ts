import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

// Build into server/ui/static so //go:embed picks it up. base=/ui/ matches the
// Go route prefix. Fixtures stay in a separate chunk via dynamic import.
export default defineConfig({
  plugins: [react()],
  base: "/ui/",
  build: {
    outDir: path.resolve(__dirname, "../server/ui/static"),
    emptyOutDir: true, // logo/favicon come from web/public
    sourcemap: false,
    cssCodeSplit: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes("fixtures") || id.includes("mockData")) return "fixtures";
          if (id.includes("node_modules")) return "vendor";
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://localhost:1984",
      "/ui": "http://localhost:1984",
      "/runs": "http://localhost:1984",
      "/info": "http://localhost:1984",
      "/health": "http://localhost:1984",
    },
  },
});
