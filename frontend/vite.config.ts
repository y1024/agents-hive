import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { resolve } from 'path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
    },
  },
  server: {
    port: 3000,
    proxy: {
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        ws: true, // 启用 WebSocket 代理
        configure: (proxy) => {
          // 忽略 WebSocket 代理的 EPIPE/ECONNRESET 错误
          // 这些错误在连接断开或后端重启时正常发生
          proxy.on('error', (err, _req, res) => {
            const code = (err as NodeJS.ErrnoException).code
            if (code === 'EPIPE' || code === 'ECONNRESET' || code === 'ECONNREFUSED') {
              return
            }
            console.error('[proxy error]', err.message)
            if (res && 'writeHead' in res && !res.headersSent) {
              ;(res as import('http').ServerResponse).writeHead(502, { 'Content-Type': 'text/plain' })
              ;(res as import('http').ServerResponse).end('Bad Gateway')
            }
          })
          proxy.on('proxyReqWs', (_proxyReq, _req, socket) => {
            socket.on('error', (err) => {
              const code = (err as NodeJS.ErrnoException).code
              if (code === 'EPIPE' || code === 'ECONNRESET') {
                return // WebSocket 断开时的正常错误，静默忽略
              }
              console.error('[ws proxy socket error]', err.message)
            })
          })
        },
      },
    },
  },
  build: {
    // 直出到 Go embed 目标目录（internal/webui/embed.go 的 //go:embed dist/* 只认相对子树）
    // 省掉旧的 `make frontend-embed` rsync 搬运步骤。
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: {
      output: {
        manualChunks: (id) => {
          if (id.includes('node_modules')) {
            if (id.includes('react') || id.includes('react-dom') || id.includes('react-router')) {
              return 'vendor-react'
            }
            if (
              id.includes('rehype-katex') ||
              id.includes('rehype') ||
              id.includes('remark') ||
              id.includes('unified') ||
              id.includes('hast') ||
              id.includes('mdast') ||
              id.includes('micromark') ||
              id.includes('vfile')
            ) {
              return 'vendor-markdown'
            }
            if (id.includes('/node_modules/katex/')) {
              return 'vendor-katex'
            }
            if (id.includes('i18next')) {
              return 'vendor-i18n'
            }
            if (id.includes('lucide-react') || id.includes('zustand')) {
              return 'vendor-ui'
            }
          }
        },
      },
    },
  },
})
