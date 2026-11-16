// validate subcommand: static validation (the SDK's validatePlugin). With --load it also loads the platform side in an isolated runtime,
// calls register, and checks that what the code actually subscribes to and registers matches the manifest.
// --load runs plugin code, so use it only on your own or reviewed directories.

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
      process.stderr.write(`Unexpected argument: ${arg}\n`)
      return 2
    }
  }
  if (!dir) {
    process.stderr.write('Usage: node main.js validate <plugin dir> [--platform-version YYYY.MM] [--load]\n')
    return 2
  }
  // Plugin code output must not get mixed into the JSON on stdout.
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
      throw new Error(`Validation does not connect to the platform (${type}); read and write state in lifecycle start or in handlers instead`)
    },
    log: (plugin, level, message) => process.stderr.write(`[${plugin}] ${level}: ${message}\n`),
  })
  const error = (message: string) => findings.push({ level: 'error', path: 'loopNow.platform', message })
  const warn = (p: string, message: string) => findings.push({ level: 'warning', path: p, message })
  try {
    const mod = await withTimeout(runAs(m.id, () => importEntry(entry)), `load ${m.platform}`)
    const register = pickRegister(mod)
    if (!register) {
      error(`${m.platform} does not export a default register function.`)
      return findings
    }
    await withTimeout(runAs(m.id, async () => register(rt.api)), 'register')
  } catch (err) {
    if (rt.violations.length === 0) error(`Could not load the platform side: ${describeError(err)}`)
  }
  for (const v of rt.violations) error(v)
  const got = rt.registrations()
  for (const e of m.events) if (!got.events.includes(e)) warn('loopNow.events', `${e} is declared, but register does not subscribe to it (the plugin may subscribe in start).`)
  for (const a of m.actions) if (!got.actions.includes(a)) warn('loopNow.actions', `Action button ${a} is declared, but register does not register a handler for it.`)
  for (const s of m.schedules) if (!got.schedules.includes(s)) warn('loopNow.schedule', `Scheduled job ${s} is declared, but register does not register a handler for it.`)
  for (const c of m.channels) if (!got.channels.includes(c)) warn('loopNow.channels', `Channel ${c} is declared, but register does not register handlers for it.`)
  if (m.http && !got.http) warn('loopNow.http', 'http is declared, but register does not register an HTTP handler.')
  await rt.stop(1_000)
  return findings
}

async function withTimeout<T>(work: Promise<T>, what: string): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined
  try {
    return await Promise.race([
      work,
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error(`${what} timed out after ${LOAD_TIMEOUT} ms`)), LOAD_TIMEOUT)
      }),
    ])
  } finally {
    clearTimeout(timer)
  }
}
