// 打包成一个自带全部依赖的 dist/main.js（Node 24，ESM）：Go 用 `node dist/main.js` 启动，不需要 node_modules。
import { readFileSync, rmSync } from 'node:fs'

import { build } from 'esbuild'

const here = new URL('.', import.meta.url).pathname
const { version } = JSON.parse(readFileSync(new URL('package.json', import.meta.url), 'utf8'))
rmSync(new URL('dist', import.meta.url), { recursive: true, force: true })
await build({
  absWorkingDir: here,
  entryPoints: ['src/main.ts'],
  outfile: 'dist/main.js',
  bundle: true,
  format: 'esm',
  platform: 'node',
  target: 'node24',
  legalComments: 'linked',
  define: { __HOST_VERSION__: JSON.stringify(version) },
  // jiti 是 CommonJS，打进 ESM 后需要 require。
  banner: { js: "import { createRequire as __loopNowCreateRequire } from 'node:module'; const require = __loopNowCreateRequire(import.meta.url);" },
  logLevel: 'warning',
})
