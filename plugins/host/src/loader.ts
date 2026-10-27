// 加载平台半边入口：与 Pi 一样用 jiti，TypeScript 不需要先构建；每次加载重新求值（不复用模块缓存），
// 卸载后再加载拿到的是新的模块状态。@loop-now/plugin-sdk 由宿主提供，插件不需要自带。

import path from 'node:path'

import { createJiti } from 'jiti/static'

import * as sdk from '../../sdk/src/index.ts'

const virtualModules = { '@loop-now/plugin-sdk': { ...sdk } }

// 转译缓存放在平台给的插件根目录下（Go 设置 LOOP_NOW_PLUGINS_ROOT）；没有时用 jiti 的默认位置。
const fsCache = process.env.LOOP_NOW_PLUGINS_ROOT ? path.join(process.env.LOOP_NOW_PLUGINS_ROOT, '.cache', 'jiti') : true

export async function importEntry(entry: string): Promise<unknown> {
  const jiti = createJiti(entry, { moduleCache: false, tryNative: false, virtualModules, fsCache })
  return jiti.import(entry, { default: true })
}

export type RegisterFn = (api: unknown) => unknown

/** 入口的默认导出（或 CommonJS 的 module.exports）是 register；也接受具名导出 register。 */
export function pickRegister(mod: unknown): RegisterFn | null {
  if (typeof mod === 'function') return mod as RegisterFn
  if (typeof mod === 'object' && mod !== null) {
    const m = mod as Record<string, unknown>
    if (typeof m.default === 'function') return m.default as RegisterFn
    if (typeof m.register === 'function') return m.register as RegisterFn
  }
  return null
}
