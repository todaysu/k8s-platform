import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:8080',
        ws: true,
        // Keep the browser-facing Host/Origin for WebSocket upgrades. The
        // platform validates Origin == Host; Vite's HTTP proxy otherwise
        // forwards Host=:8080 while the browser sends Origin=:5173.
        configure: (proxy) => {
          proxy.on('proxyReqWs', (proxyReq, request) => {
            const origin = request.headers.origin
            if (origin) {
              try {
                proxyReq.setHeader('host', new URL(origin).host)
              } catch {
                // Ignore malformed Origin; the platform will reject it.
              }
            }
          })
        },
      },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test/setup.ts',
  },
})
