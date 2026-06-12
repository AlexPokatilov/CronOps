import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In dev the API is proxied to a locally running cronops-server (port 8090),
// so the UI and API share an origin and the session cookie just works.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": "http://localhost:8090",
    },
  },
});
