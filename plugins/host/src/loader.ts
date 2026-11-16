// Loads the platform-side entry: uses jiti like Pi does, so TypeScript needs no build step. Every load evaluates the module again
// (the module cache is not reused), so loading again after an unload gets fresh module state. The host provides @loop-now/plugin-sdk;
// plugins need not bring their own.

import path from 'node:path'

import { createJiti } from 'jiti/static'

import * as sdk from '../../sdk/src/index.ts'

const virtualModules = { '@loop-now/plugin-sdk': { ...sdk } }

// The transpile cache lives under the plugins root the platform provides (Go sets LOOP_NOW_PLUGINS_ROOT);
// without it, jiti's default location is used.
const fsCache = process.env.LOOP_NOW_PLUGINS_ROOT ? path.join(process.env.LOOP_NOW_PLUGINS_ROOT, '.cache', 'jiti') : true

export async function importEntry(entry: string): Promise<unknown> {
  const jiti = createJiti(entry, { moduleCache: false, tryNative: false, virtualModules, fsCache })
  return jiti.import(entry, { default: true })
}

export type RegisterFn = (api: unknown) => unknown

/** The entry's default export (or CommonJS module.exports) is register; a named export register is accepted too. */
export function pickRegister(mod: unknown): RegisterFn | null {
  if (typeof mod === 'function') return mod as RegisterFn
  if (typeof mod === 'object' && mod !== null) {
    const m = mod as Record<string, unknown>
    if (typeof m.default === 'function') return m.default as RegisterFn
    if (typeof m.register === 'function') return m.register as RegisterFn
  }
  return null
}
