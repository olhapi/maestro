import { defineConfig, mergeConfig } from 'vitest/config'
import path from 'node:path'

import viteConfig from './vite.config'

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'jsdom',
      globals: true,
      css: true,
      setupFiles: [path.resolve(__dirname, './src/test/setup.ts')],
      // Keep the route-heavy jsdom suite stable under the full pre-push coverage pass.
      maxWorkers: 2,
      hookTimeout: 15000,
      testTimeout: 15000,
    },
    coverage: {
      provider: 'v8',
      all: true,
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.test.*',
        'src/**/*.spec.*',
        'src/test/**',
        'src/**/*.d.ts',
      ],
      thresholds: {
        branches: 90,
        functions: 90,
        lines: 90,
        statements: 90,
      },
    },
  }),
)
