// Builds the publishable dist/: esbuild produces ESM, tsc produces the declaration files.
import { execFileSync } from 'node:child_process'
import { readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'

import { build } from 'esbuild'

const here = new URL('.', import.meta.url).pathname
rmSync(new URL('dist', import.meta.url), { recursive: true, force: true })
await build({
  absWorkingDir: here,
  entryPoints: ['src/index.ts', 'src/validate.ts', 'src/cli.ts'],
  outdir: 'dist',
  bundle: true,
  splitting: true,
  format: 'esm',
  platform: 'node',
  target: 'node24',
  packages: 'external',
  chunkNames: 'chunks/[name]-[hash]',
  logLevel: 'warning',
})
const tsc = createRequire(import.meta.url).resolve('typescript/bin/tsc')
execFileSync(process.execPath, [tsc, '-p', 'tsconfig.build.json'], { cwd: here, stdio: 'inherit' })
// Rewrite relative imports in the declaration files to .js, matching the published JavaScript.
for (const name of readdirSync(new URL('dist', import.meta.url))) {
  if (!name.endsWith('.d.ts')) continue
  const file = new URL(`dist/${name}`, import.meta.url)
  writeFileSync(file, readFileSync(file, 'utf8').replace(/(from\s+['"]\.{1,2}\/[^'"]+)\.ts(['"])/g, '$1.js$2'))
}
