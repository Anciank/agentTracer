import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

// Build into server/ui/static so //go:embed picks it up. base=/ui/ matches the
// Go route prefix. Fixture module is only dynamically imported behind
// import.meta.env.DEV || VITE_MOCK===1 so production builds tree-shake it out.
export default defineConfig({
  plugins: [react()],
  base: "/ui/",
  build: {
    outDir: path.resolve(__dirname, "../server/ui/static"),
    emptyOutDir: true,
    sourcemap: false,
    cssCodeSplit: true,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes("node_modules")) return "vendor";
        },
      },
    },
  },
  server: {
    port: 5173,
    proxy: {
      // Only API/health — /ui is served by Vite itself (base=/ui/).
      "/api": "http://localhost:1984",
      "/runs": "http://localhost:1984",
      "/info": "http://localhost:1984",
      "/health": "http://localhost:1984",
    },
  },
});
