import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
  },
  server: {
    // Proxies API calls to the Go backend during `npm run dev`, matching
    // LibriNode/AcerviNode's own dev-server convention (7845/7846;
    // CantiNode's default port is 7847).
    proxy: {
      '/api': 'http://localhost:7847',
    },
  },
})
