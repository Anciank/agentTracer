import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig({ plugins: [react()], server: { port: 5173, proxy: { "/ui": "http://localhost:1984", "/runs": "http://localhost:1984", "/info": "http://localhost:1984" } } });
