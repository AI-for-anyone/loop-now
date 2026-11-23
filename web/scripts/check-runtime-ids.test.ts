import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
// @ts-expect-error the script is an ESM module without type declarations
import { findRuntimeComparisons } from './check-runtime-ids.mjs'

const dirs: string[] = []

const tsconfig = JSON.stringify({
  compilerOptions: { target: 'ES2022', module: 'ESNext', moduleResolution: 'bundler', strict: true, noEmit: true, skipLibCheck: true, types: [] },
  include: ['src'],
  exclude: ['src/**/*.test.ts', 'src/test'],
})
const schema = (runtimes: string) => `export interface components { schemas: { AgentRuntime: ${runtimes}; CommandSource: "platform" | "pi" } }\n`

function project(files: Record<string, string>, runtimes = '"pi" | "claude-code"'): string {
  const dir = mkdtempSync(join(tmpdir(), 'runtime-ids-'))
  dirs.push(dir)
  const all = { 'tsconfig.app.json': tsconfig, 'src/api/schema.gen.ts': schema(runtimes), ...files }
  for (const [path, content] of Object.entries(all)) {
    mkdirSync(join(dir, path, '..'), { recursive: true })
    writeFileSync(join(dir, path), content)
  }
  return dir
}

afterEach(() => {
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true })
})

const header = "import type { components } from './api/schema.gen'\ntype S = components['schemas']\ntype Runtime = S['AgentRuntime']\n"
const found = (root: string) => findRuntimeComparisons(root).found.map((item: { file: string; line: number; id: string }) => `${item.file}:${item.line} ${item.id}`)

describe('check-runtime-ids', () => {
  it('reports a runtime id compared with a runtime value or a string, from either side, in a switch, or through a constant', () => {
    const root = project({
      'src/page.ts': header + [
        "const CLAUDE = 'claude-code'",
        'export function page(agent: { runtime: Runtime }, provider: { runtime?: Runtime } | undefined, providerId: string, runtime: Runtime) {',
        "  const a = agent.runtime === 'claude-code'",
        "  const b = provider?.runtime !== 'pi'",
        "  const c = 'pi' == runtime",
        '  const d = providerId === CLAUDE',
        "  switch (runtime) { case 'claude-code': return a }",
        '  return b || c || d',
        '}',
      ].join('\n'),
    })
    expect(found(root)).toEqual(['src/page.ts:6 claude-code', 'src/page.ts:7 pi', 'src/page.ts:8 pi', 'src/page.ts:9 claude-code', 'src/page.ts:10 claude-code'])
  })

  it('leaves alone other types that share a word, runtime-to-runtime comparisons, per-runtime tables, the lookup and tests', () => {
    const root = project({
      'src/other.ts': header + [
        "export function other(source: S['CommandSource'], a: Runtime, b: Runtime, name: string) {",
        "  const names: Record<Runtime, string> = { pi: 'Pi', 'claude-code': 'Claude Code' }",
        "  return [source === 'pi', a === b, name === 'pinned', names[a]]",
        '}',
      ].join('\n'),
      'src/lib/runtime.ts': `${header.replaceAll('./api', '../api')}export const isPi = (runtime: Runtime) => runtime === 'pi'\n`,
      'src/page.test.ts': `${header}export const t = (runtime: Runtime) => runtime === 'pi'\n`,
      'src/test/fixtures.ts': `${header.replaceAll('./api', '../api')}export const f = (runtime: Runtime) => runtime === 'claude-code'\n`,
    })
    expect(findRuntimeComparisons(root)).toEqual({ ids: ['pi', 'claude-code'], found: [] })
  })

  it('takes the runtime ids from the generated API types, so a new runtime is covered as soon as the contract has it', () => {
    const root = project({ 'src/page.ts': `${header}export const codex = (runtime: Runtime) => runtime === 'codex'\n` }, '"pi" | "claude-code" | "codex"')
    expect(found(root)).toEqual(['src/page.ts:4 codex'])
  })
})
