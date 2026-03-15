import { defineConfig, mergeConfig } from 'vitest/config'
import path from 'node:path'

import viteConfig from './vite.config'

export default mergeConfig(
  viteConfig,
  defineConfig({
    esbuild: {
      jsx: 'automatic',
    },
    test: {
      environment: 'jsdom',
      globals: true,
      css: true,
      setupFiles: [path.resolve(__dirname, './src/test/setup.ts')],
    },
  }),
)
