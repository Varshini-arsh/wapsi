import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      // The seeded four-role walkthrough runs on the demo gateway by default.
      // Set WAPSI_API=http://localhost:4000 to point the UI at the live ledger.
      "/api": process.env.WAPSI_API || "http://localhost:4001",
    },
  },
});
