import { defineConfig } from 'vitest/config'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'
import { resolve } from 'path'
import { readFileSync } from 'fs'

const appVersion = readFileSync(resolve(__dirname, '..', 'VERSION'), 'utf8').trim()
const releaseBaseURL = `https://github.com/Xsxdot/super-dev/releases/download/v${appVersion}`

export default defineConfig({
  plugins: [vue(), tailwindcss()],
  resolve: {
    alias: { '@': resolve(__dirname, 'src') },
  },
  define: {
    __SUPERDEV_RELEASE_BASE_URL__: JSON.stringify(releaseBaseURL),
  },
  clearScreen: false,
  server: { port: 6688, strictPort: true },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: [resolve(__dirname, 'src/test-utils/setup.ts')],
  },
})
