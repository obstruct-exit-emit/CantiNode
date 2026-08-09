import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The dev server proxies API calls to the Go backend on LibriNode's port,
// so `npm run dev` + `go run ./cmd/librinode` work together out of the box.
// Production builds land in web/dist, which the Go binary will embed/serve.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": "http://localhost:7845",
      "/ping": "http://localhost:7845",
    },
  },
});
