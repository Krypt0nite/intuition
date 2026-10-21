import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { viteSingleFile } from 'vite-plugin-singlefile'

// `npm run dev`          → proxies /api and /ws to the Go server on :8080
//                          (falls back to demo mode if it isn't running)
// `npm run build`        → production build served by the Go binary
// `npm run build:demo`   → one self-contained HTML file, offline demo mode
export default defineConfig(({ mode }) => ({
  plugins: [react(), ...(mode === 'demo' ? [viteSingleFile()] : [])],
  define: {
    // Production builds don't ship the answer pack to browsers (anti-cheat).
    __DEMO_FALLBACK__: JSON.stringify(mode !== 'production' || process.env.VITE_DEMO_FALLBACK === 'true'),
    __FORCE_DEMO__: JSON.stringify(mode === 'demo'),
  },
  build: { outDir: mode === 'demo' ? 'dist-demo' : 'dist' },
  server: {
    proxy: {
      '/api': 'http://localhost:8080',
      '/ws': { target: 'ws://localhost:8080', ws: true },
    },
  },
  test: { environment: 'node' },
}))
