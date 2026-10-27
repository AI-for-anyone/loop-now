// validate 子命令：静态校验（SDK 的 validatePlugin），加 --load 时再在隔离的运行时里加载平台半边、调用 register，
// 核对代码实际订阅与注册的和清单是否一致。--load 会执行插件代码，只用于自己的或已审阅的目录。

import path from 'node:path'

import { validatePlugin, type Finding } from '../../sdk/src/validate.ts'
import { importEntry, pickRegister } from './loader.ts'
import { installOutput, runAs } from './output.ts'
import { describeError, parseLoad, PluginRuntime } from './runtime.ts'

const LOAD_TIMEOUT = 10_000

export async function runValidate(args: string[]): Promise<number> {
  let dir: string | undefined
  let platformVersion: string | undefined
  let load = false
  for (let i = 0; i < args.length; i++) {
    const arg = args[i]!
    if (arg === '--platform-version') platformVersion = args[++i]
    else if (arg === '--load') load = true
    else if (dir === undefined) dir = arg
    else {
      process.stderr.write(`多余的参数：${arg}\n`)
      return 2
    }
  }
  if (!dir) {
    process.stderr.write('用法：node main.js validate <插件目录> [--platform-version YYYY.MM] [--load]\n')
    return 2
  }
  // 插件代码的输出不能混进标准输出的 JSON。
  const output = installOutput({ log: (plugin, level, message) => process.stderr.write(`[${plugin}] ${level}: ${message}\n`), mask: (_, t) => t })
  const result = validatePlugin(dir, { platformVersion })
  if (load && result.ok && result.manifest?.platform) {
    result.findings.push(...(await dryRun(result.dir, result.manifest)))
    result.ok = !result.findings.some((f) => f.level === 'error')
  }
  output.protocol(`${JSON.stringify(result, null, 2)}\n`)
  return result.ok ? 0 : 1
}

type Manifest = NonNullable<ReturnType<typeof validatePlugin>['manifest']>

async function dryRun(root: string, m: Manifest): Promise<Finding[]> {
  const findings: Finding[] = []
  const entry = path.join(root, m.platform!)
  const payload = parseLoad(m.id, {
    id: m.id,
    version: m.version,
    root,
    entry,
    dataDir: '',
    manifest: {
      events: m.events,
      permissions: m.permissions,
      actions: m.actions.map((id) => ({ id })),
      schedules: m.schedules.map((id) => ({ id })),
      channels: m.channels.map((id) => ({ id, manageIdentities: m.identityChannels.includes(id) })),
      http: m.http,
    },
    config: {},
  })
  const rt = new PluginRuntime(payload, {
    request: async (type) => {
      throw new Error(`校验时不连接平台（${type}）；状态读写请放到 lifecycle start 或处理函数里`)
    },
    log: (plugin, level, message) => process.stderr.write(`[${plugin}] ${level}: ${message}\n`),
  })
  const error = (message: string) => findings.push({ level: 'error', path: 'loopNow.platform', message })
  const warn = (p: string, message: string) => findings.push({ level: 'warning', path: p, message })
  try {
    const mod = await withTimeout(runAs(m.id, () => importEntry(entry)), `加载 ${m.platform}`)
    const register = pickRegister(mod)
    if (!register) {
      error(`${m.platform} 没有默认导出 register 函数`)
      return findings
    }
    await withTimeout(runAs(m.id, async () => register(rt.api)), 'register')
  } catch (err) {
    if (rt.violations.length === 0) error(`加载平台半边失败：${describeError(err)}`)
  }
  for (const v of rt.violations) error(v)
  const got = rt.registrations()
  for (const e of m.events) if (!got.events.includes(e)) warn('loopNow.events', `声明了 ${e}，但 register 里没有订阅（可能在 start 里订阅）`)
  for (const a of m.actions) if (!got.actions.includes(a)) warn('loopNow.actions', `声明了操作按钮 ${a}，但 register 里没有注册`)
  for (const s of m.schedules) if (!got.schedules.includes(s)) warn('loopNow.schedule', `声明了定时任务 ${s}，但 register 里没有注册`)
  for (const c of m.channels) if (!got.channels.includes(c)) warn('loopNow.channels', `声明了渠道 ${c}，但 register 里没有注册`)
  if (m.http && !got.http) warn('loopNow.http', '声明了 http，但 register 里没有注册 HTTP 处理函数')
  await rt.stop(1_000)
  return findings
}

async function withTimeout<T>(work: Promise<T>, what: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  try {
    return await Promise.race([
      work,
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error(`${what}超过 ${LOAD_TIMEOUT} 毫秒`)), LOAD_TIMEOUT)
      }),
    ])
  } finally {
    clearTimeout(timer)
  }
}
