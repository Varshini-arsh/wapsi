import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 5173,
    proxy: {
      // WAPSI_API lets the UI service point at the demo gateway (4001)
      // or the fabric gateway (4000). Default: fabric gateway.
      "/api": process.env.WAPSI_API || "http://localhost:4000",
    },
  },
});
