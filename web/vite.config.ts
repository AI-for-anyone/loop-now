import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  // The server's default origin allowlist is http://localhost:5173, so fail right away when the port is taken instead of switching ports
  // automatically.
  server: { host: 'localhost', port: 5173, strictPort: true },
  // Font subsets are downloaded on demand by unicode-range, not inlined into the CSS.
  build: { assetsInlineLimit: (file) => (file.endsWith('.woff2') ? false : undefined) },
})
