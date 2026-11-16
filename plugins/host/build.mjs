// Bundle into a single dist/main.js with all dependencies included (Node 24, ESM): Go starts it with `node dist/main.js`, no node_modules needed.
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
  // jiti is CommonJS, so it needs require once bundled into ESM.
  banner: { js: "import { createRequire as __loopNowCreateRequire } from 'node:module'; const require = __loopNowCreateRequire(import.meta.url);" },
  logLevel: 'warning',
})
