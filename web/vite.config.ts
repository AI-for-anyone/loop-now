import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { '@': fileURLToPath(new URL('./src', import.meta.url)) },
  },
  // 服务端默认的来源白名单是 http://localhost:5173，端口被占用时直接报错，不自动换端口。
  server: { host: 'localhost', port: 5173, strictPort: true },
  // 字体切片按 unicode-range 按需下载，不内联进 CSS。
  build: { assetsInlineLimit: (file) => (file.endsWith('.woff2') ? false : undefined) },
})
