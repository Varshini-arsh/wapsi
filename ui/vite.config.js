import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      // Presentation target: the Fabric gateway. The demo gateway remains
      // available directly on port 4001 for fallback testing.
      "/api": "http://localhost:4000",
    },
  },
});
