import { configDefaults, defineConfig, mergeConfig } from 'vitest/config'
import viteConfig from './vite.config.ts'

// Tests run in the English UI (project "en-US"). A small supplement in src/zh-CN/ runs in the Chinese UI (project "zh-CN"): see
// src/zh-CN/README.md for what it covers. The two projects never share a file.
const zhSupplement = ['src/zh-CN/**/*.test.{ts,tsx}']
// Test files not moved to the English UI yet; they still run in the Chinese UI. Transitional: empty once the move is done.
const notYetEnglish = [
  'scripts/**/*.test.ts',
  ...['api', 'app', 'i18n', 'lib'].map((dir) => `src/${dir}/**/*.test.{ts,tsx}`),
  ...['console-inbox', 'desktop', 'folders', 'images', 'inbox', 'project-credentials', 'prompts', 'settings', 'sleep'].map(
    (dir) => `src/features/${dir}/**/*.test.{ts,tsx}`,
  ),
]

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      environment: 'jsdom',
      css: false,
      restoreMocks: true,
      projects: [
        {
          extends: true,
          test: {
            name: 'en-US',
            include: ['src/**/*.test.{ts,tsx}', 'scripts/**/*.test.ts'],
            exclude: [...configDefaults.exclude, ...zhSupplement, ...notYetEnglish],
            setupFiles: ['./src/test/locale-en.ts', './src/test/setup.ts'],
          },
        },
        {
          extends: true,
          test: {
            name: 'zh-CN',
            include: [...zhSupplement, ...notYetEnglish],
            setupFiles: ['./src/test/locale-zh.ts', './src/test/setup.ts'],
          },
        },
      ],
    },
  }),
)
