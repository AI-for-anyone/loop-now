// Validation of a plugin directory (plugins/SPEC.md §2): errors match the rules by which the server's internal/pluginmanifest refuses
// to install; it also gives warnings that do not block installing but that authors should usually fix.
// JSON Schema gets structural checks only; the server compiles it fully at install time.

import { existsSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'

import { EVENTS, PERMISSIONS } from './events.ts'

export type FindingLevel = 'error' | 'warning'

export interface Finding {
  level: FindingLevel
  /** Location in the manifest, such as loopNow.events[2]; an empty string for problems with the whole directory. */
  path: string
  message: string
}

/**
 * Localized text (plugins/SPEC.md §2): a string (shown for every language), or an object keyed by language tag,
 * such as { "zh-CN": "…", "en-US": "…" }.
 */
export type LocalizedText = string | Record<string, string>

export interface ValidatedManifest {
  id: string
  name: string
  version: string
  displayName: LocalizedText
  description: LocalizedText
  platform: string | null
  extensions: string[]
  skills: string[]
  events: string[]
  permissions: string[]
  actions: string[]
  schedules: string[]
  channels: string[]
  /** Channels that declare manageIdentities and so can manage external identities. */
  identityChannels: string[]
  http: boolean
  mcpServers: string[]
  bin: string[]
}

export interface ValidationResult {
  ok: boolean
  dir: string
  manifest: ValidatedManifest | null
  findings: Finding[]
}

export interface ValidateOptions {
  /** The platform release version (YYYY.MM); when omitted or not in that form, minPlatformVersion is not checked. */
  platformVersion?: string
}

const ID_PATTERN = /^[a-z0-9]([a-z0-9-]{0,62}[a-z0-9])?$/
const VERSION_PATTERN = /^[0-9A-Za-z][0-9A-Za-z.+-]{0,63}$/
const KIND_PATTERN = /^[a-z][a-z0-9_]{0,31}$/
const SLUG_PATTERN = /^[a-z0-9][a-z0-9_-]{0,63}$/
/** Language tag per BCP 47: a 2 to 3 letter primary language, then zero or more hyphen-separated subtags (zh-CN, en-US, en, zh-Hant-TW). */
const LANGUAGE_TAG = /^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$/
const LOCALIZED_RULE = '须是字符串，或以语言标签（如 zh-CN、en-US）为键、非空字符串为值的对象，至少一项'
const WHERE = ['agent', 'task', 'project', 'identity', 'global']
const SCHEMA_TYPES = ['object', 'array', 'string', 'number', 'integer', 'boolean', 'null']
const ENTRY_EXTENSIONS = ['.ts', '.mts', '.cts', '.js', '.mjs', '.cjs']
// Packages Pi provides to extensions (Pi docs packages.md) and the SDK the host provides to the platform side: these belong in
// peerDependencies and are not installed with the plugin.
const HOST_PROVIDED = [
  '@loop-now/plugin-sdk',
  '@earendil-works/pi-ai',
  '@earendil-works/pi-agent-core',
  '@earendil-works/pi-coding-agent',
  '@earendil-works/pi-tui',
  'typebox',
]

type Json = Record<string, unknown>

/**
 * Whether an mcpServers entry can be handed to Pi (the transport is chosen the same way as Pi's validateMcpServerConfig; the server's
 * internal/pluginmanifest does the same):
 * with a url and type omitted or http or streamable-http, it is streamable HTTP, and the url must spell out http:// or https:// plus a host
 * (URL normalizes http:foo to http://foo/ and the server checks for a non-empty host, so the spelling is checked separately);
 * otherwise, with a non-empty command and type omitted or stdio, it is stdio. Returns null when acceptable, otherwise which kind of problem.
 */
function mcpEntryProblem(cfg: unknown): 'sse' | 'type' | 'entry' | null {
  if (!isObject(cfg)) return 'entry'
  const { type, url, command } = cfg
  if (type === 'sse') return 'sse'
  if (type !== undefined && type !== 'stdio' && type !== 'http' && type !== 'streamable-http') return 'type'
  if (typeof url === 'string' && type !== 'stdio') return /^https?:\/\/[^/]/i.test(url) && URL.canParse(url) ? null : 'entry'
  return typeof command === 'string' && command !== '' && type !== 'http' && type !== 'streamable-http' ? null : 'entry'
}

function isObject(v: unknown): v is Json {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}

function strings(v: unknown): v is string[] {
  return Array.isArray(v) && v.every((s) => typeof s === 'string')
}

/**
 * Reads a localized field (description, loopNow.displayName): fallback when missing; a string as is; an object must have at least one entry,
 * keyed by language tag, with values that are non-empty after trimming whitespace. Any other form is an error and is treated as fallback.
 * Same rules as the server's internal/pluginmanifest.
 */
function localizedField(v: unknown, field: string, fallback: string, error: (p: string, m: string) => void): LocalizedText {
  if (v === undefined) return fallback
  if (typeof v === 'string') return v
  if (isObject(v) && Object.keys(v).length > 0 && Object.entries(v).every(([k, t]) => LANGUAGE_TAG.test(k) && typeof t === 'string' && t.trim() !== '')) {
    return v as Record<string, string>
  }
  error(field, `${field} ${LOCALIZED_RULE}`)
  return fallback
}

export function validatePlugin(dir: string, options: ValidateOptions = {}): ValidationResult {
  const root = path.resolve(dir)
  const findings: Finding[] = []
  const error = (p: string, message: string) => findings.push({ level: 'error', path: p, message })
  const warn = (p: string, message: string) => findings.push({ level: 'warning', path: p, message })
  const done = (manifest: ValidatedManifest | null): ValidationResult => ({
    ok: !findings.some((f) => f.level === 'error'),
    dir: root,
    manifest,
    findings,
  })

  if (!existsSync(root) || !statSync(root).isDirectory()) {
    error('', `${root} 不是目录`)
    return done(null)
  }
  const pkgPath = path.join(root, 'package.json')
  if (!existsSync(pkgPath)) {
    error('', '插件目录没有 package.json')
    return done(null)
  }
  let pkg: unknown
  try {
    pkg = JSON.parse(readFileSync(pkgPath, 'utf8'))
  } catch (err) {
    error('', `package.json 不是合法的 JSON：${(err as Error).message}`)
    return done(null)
  }
  if (!isObject(pkg)) {
    error('', 'package.json 须是对象')
    return done(null)
  }
  const pi = pkg.pi
  const ln = pkg.loopNow
  if (pi === undefined && ln === undefined) {
    error('', 'package.json 既没有 pi 段也没有 loopNow 段，不是插件')
    return done(null)
  }
  if (pi !== undefined && !isObject(pi)) error('pi', 'pi 须是对象')
  if (ln !== undefined && !isObject(ln)) error('loopNow', 'loopNow 须是对象')
  const piObj = isObject(pi) ? pi : {}
  const lnObj = isObject(ln) ? ln : {}

  const name = typeof pkg.name === 'string' ? pkg.name : ''
  const version = typeof pkg.version === 'string' ? pkg.version : ''
  const description = localizedField(pkg.description, 'description', '', error)
  if (!VERSION_PATTERN.test(version)) error('version', 'version 必须是 1 到 64 个字母、数字、点、加号或连字符')

  const list = (obj: Json, key: string, field: string): string[] => {
    const v = obj[key]
    if (v === undefined) return []
    if (!strings(v)) {
      error(field, `${field} 须是字符串数组`)
      return []
    }
    return v
  }
  const extensions = list(piObj, 'extensions', 'pi.extensions')
  const skills = list(piObj, 'skills', 'pi.skills')
  const events = list(lnObj, 'events', 'loopNow.events')
  const permissions = list(lnObj, 'permissions', 'loopNow.permissions')
  const bin = list(lnObj, 'bin', 'loopNow.bin')

  let id = typeof lnObj.id === 'string' ? lnObj.id : ''
  if (lnObj.id !== undefined && typeof lnObj.id !== 'string') error('loopNow.id', 'loopNow.id 须是字符串')
  if (id === '') {
    // A standard Pi package with only a pi section: use the npm name without its scope as the id (same as the server).
    id = (name.split('/').pop() ?? '')
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 64)
      .replace(/-+$/, '')
    if (ln !== undefined) warn('loopNow.id', `没有 loopNow.id，将用 npm 名得到 ${JSON.stringify(id)}；建议显式声明，安装后不可改`)
  }
  if (!ID_PATTERN.test(id)) error('loopNow.id', 'loopNow.id 必须是 1 到 64 个小写字母、数字或连字符，不以连字符开头或结尾')
  const declaredName = localizedField(lnObj.displayName, 'loopNow.displayName', '', error)
  const displayName = declaredName === '' ? name || id : declaredName

  const minPlatform = lnObj.minPlatformVersion
  if (minPlatform !== undefined && minPlatform !== null) {
    if (typeof minPlatform !== 'string') error('loopNow.minPlatformVersion', 'loopNow.minPlatformVersion 须是字符串')
    else if (releaseParts(minPlatform) === null) error('loopNow.minPlatformVersion', 'loopNow.minPlatformVersion 须是 YYYY.MM 形式的发布版本')
    else if (options.platformVersion && !versionAtLeast(options.platformVersion, minPlatform)) {
      error('loopNow.minPlatformVersion', `插件要求平台版本不低于 ${minPlatform}，当前为 ${options.platformVersion}`)
    }
  }

  const platform = typeof lnObj.platform === 'string' && lnObj.platform !== '' ? lnObj.platform : null
  if (lnObj.platform !== undefined && typeof lnObj.platform !== 'string') error('loopNow.platform', 'loopNow.platform 须是字符串')
  const mcpServers = isObject(lnObj.mcpServers) ? lnObj.mcpServers : {}
  if (lnObj.mcpServers !== undefined && !isObject(lnObj.mcpServers)) error('loopNow.mcpServers', 'loopNow.mcpServers 须是对象')

  if (extensions.length === 0 && skills.length === 0 && platform === null && Object.keys(mcpServers).length === 0 && bin.length === 0) {
    error('', '插件既没有 Agent 侧（扩展、Skill、MCP 服务器、CLI）也没有平台侧')
  }

  const seen = new Set<string>()
  events.forEach((e, i) => {
    if (!Object.hasOwn(EVENTS, e)) error(`loopNow.events[${i}]`, `${JSON.stringify(e)} 不是平台的事件`)
    else if (seen.has(e)) error(`loopNow.events[${i}]`, `${JSON.stringify(e)} 重复`)
    seen.add(e)
  })
  permissions.forEach((p, i) => {
    if (!(PERMISSIONS as readonly string[]).includes(p)) error(`loopNow.permissions[${i}]`, `${JSON.stringify(p)} 不是可用的范围`)
  })
  if (lnObj.failClosed !== undefined && typeof lnObj.failClosed !== 'boolean') error('loopNow.failClosed', 'loopNow.failClosed 须是布尔值')
  if (lnObj.failClosed === true && !events.some((e) => (EVENTS as Record<string, string>)[e] === 'intercept')) {
    warn('loopNow.failClosed', 'failClosed 只影响拦截类事件，但清单没有订阅拦截类事件')
  }

  const actions = Array.isArray(lnObj.actions) ? lnObj.actions : []
  if (lnObj.actions !== undefined && !Array.isArray(lnObj.actions)) error('loopNow.actions', 'loopNow.actions 须是数组')
  const schedules = Array.isArray(lnObj.schedule) ? lnObj.schedule : []
  if (lnObj.schedule !== undefined && !Array.isArray(lnObj.schedule)) error('loopNow.schedule', 'loopNow.schedule 须是数组')
  const channels = Array.isArray(lnObj.channels) ? lnObj.channels : []
  if (lnObj.channels !== undefined && !Array.isArray(lnObj.channels)) error('loopNow.channels', 'loopNow.channels 须是数组')
  const http = lnObj.http === true
  if (lnObj.http !== undefined && typeof lnObj.http !== 'boolean') error('loopNow.http', 'loopNow.http 须是布尔值')

  if ((events.length > 0 || actions.length > 0 || channels.length > 0 || http || schedules.length > 0) && platform === null) {
    error('loopNow.platform', '订阅事件、操作按钮、渠道、HTTP 与定时都由平台侧处理，须声明 loopNow.platform')
  }
  if (platform !== null && checkPath(root, platform, 'loopNow.platform', false, error)) {
    if (!ENTRY_EXTENSIONS.includes(path.extname(platform))) warn('loopNow.platform', `平台侧入口的扩展名通常是 ${ENTRY_EXTENSIONS.join('、')}`)
  }
  extensions.forEach((p, i) => checkPath(root, p, `pi.extensions[${i}]`, true, error))
  skills.forEach((p, i) => checkPath(root, p, `pi.skills[${i}]`, true, error))
  bin.forEach((p, i) => checkPath(root, p, `loopNow.bin[${i}]`, true, error))

  for (const [server, cfg] of Object.entries(mcpServers)) {
    const problem = SLUG_PATTERN.test(server) ? mcpEntryProblem(cfg) : 'entry'
    if (problem === null) continue
    const name = JSON.stringify(server)
    error(
      `loopNow.mcpServers.${server}`,
      problem === 'sse'
        ? `${name} 不支持 SSE，请用 streamable HTTP 的 url`
        : problem === 'type'
          ? `${name} 的 type 只能是 stdio、http 或 streamable-http`
          : `${name} 须是带 command 的 stdio 条目或带 http/https url 的 streamable HTTP 条目，名称为小写字母、数字、下划线或连字符`,
    )
  }

  if (lnObj.configSchema !== undefined) {
    if (!isObject(lnObj.configSchema)) error('loopNow.configSchema', 'loopNow.configSchema 须是对象')
    else {
      if (lnObj.configSchema.type !== undefined && lnObj.configSchema.type !== 'object') error('loopNow.configSchema.type', 'loopNow.configSchema 的 type 须为 object')
      checkSchema(lnObj.configSchema, 'loopNow.configSchema', { secret: true, scope: true }, error, warn)
    }
  }

  const accounts = Array.isArray(lnObj.accounts) ? lnObj.accounts : []
  if (lnObj.accounts !== undefined && !Array.isArray(lnObj.accounts)) error('loopNow.accounts', 'loopNow.accounts 须是数组')
  const kinds = new Set<string>()
  accounts.forEach((a: unknown, i) => {
    const p = `loopNow.accounts[${i}]`
    if (!isObject(a) || typeof a.kind !== 'string' || !KIND_PATTERN.test(a.kind) || kinds.has(a.kind)) {
      error(p, 'loopNow.accounts 的 kind 须唯一，由小写字母、数字或下划线组成，以字母开头')
      return
    }
    kinds.add(a.kind)
    if (!isObject(a.fields)) error(`${p}.fields`, `loopNow.accounts 的 ${a.kind} 缺少 fields`)
    else checkSchema(a.fields, `${p}.fields`, { secret: true, scope: false }, error, warn)
  })

  const actionIds = new Set<string>()
  actions.forEach((a: unknown, i) => {
    const p = `loopNow.actions[${i}]`
    if (!isObject(a) || typeof a.id !== 'string' || !SLUG_PATTERN.test(a.id) || actionIds.has(a.id) || typeof a.label !== 'string' || a.label === '' || !strings(a.where) || a.where.length === 0) {
      error(p, 'loopNow.actions 的每项须有唯一的 id、label 与 where')
      return
    }
    actionIds.add(a.id)
    if (a.where.some((w) => !WHERE.includes(w))) error(`${p}.where`, `loopNow.actions 的 where 只能是 ${WHERE.join('、')}`)
    if (a.fields !== undefined) {
      if (!isObject(a.fields)) error(`${p}.fields`, `loopNow.actions 的 ${a.id} fields 须是对象`)
      else checkSchema(a.fields, `${p}.fields`, { secret: false, scope: false }, error, warn)
    }
  })
  const channelIds: string[] = []
  const identityChannels: string[] = []
  channels.forEach((c: unknown, i) => {
    if (!isObject(c) || typeof c.id !== 'string' || !SLUG_PATTERN.test(c.id) || typeof c.label !== 'string' || c.label === '') {
      error(`loopNow.channels[${i}]`, 'loopNow.channels 的每项须有 id 与 label')
      return
    }
    channelIds.push(c.id)
    if (c.manageIdentities !== undefined && typeof c.manageIdentities !== 'boolean') error(`loopNow.channels[${i}].manageIdentities`, 'manageIdentities 须是布尔值')
    if (c.manageIdentities === true) {
      identityChannels.push(c.id)
      if (!permissions.includes('identities.manage')) error(`loopNow.channels[${i}].manageIdentities`, '代管外部身份的渠道须在 loopNow.permissions 里声明 identities.manage')
    }
  })
  const scheduleIds: string[] = []
  schedules.forEach((s: unknown, i) => {
    if (!isObject(s) || typeof s.id !== 'string' || !SLUG_PATTERN.test(s.id) || typeof s.cron !== 'string' || s.cron.trim().split(/\s+/).length !== 5) {
      error(`loopNow.schedule[${i}]`, 'loopNow.schedule 的每项须有 id 与五段的 cron')
      return
    }
    scheduleIds.push(s.id)
  })

  // The following are warnings: they do not block installing.
  const keywords = strings(pkg.keywords) ? pkg.keywords : []
  if (ln !== undefined && !keywords.includes('loop-now-plugin')) warn('keywords', '建议在 keywords 里加上 loop-now-plugin，便于在市场与 npm 上被找到')
  if (extensions.length > 0 && !keywords.includes('pi-package')) warn('keywords', '带 Pi 扩展的插件建议加上 pi-package 关键字（Pi 包的约定）')
  if (description === '') warn('description', '缺少 description，安装确认与插件页会显示为空')
  if (ln !== undefined && (lnObj.displayName === undefined || lnObj.displayName === '')) warn('loopNow.displayName', `缺少 loopNow.displayName，将显示为 ${JSON.stringify(displayName)}`)
  const deps = isObject(pkg.dependencies) ? pkg.dependencies : {}
  for (const dep of HOST_PROVIDED) {
    if (Object.hasOwn(deps, dep)) warn(`dependencies.${dep}`, `${dep} 由宿主或 Pi 提供，应放在 peerDependencies（"*"），不要随插件安装`)
  }
  if (Object.keys(deps).length > 0 && !existsSync(path.join(root, 'package-lock.json')) && !existsSync(path.join(root, 'npm-shrinkwrap.json'))) {
    warn('dependencies', '有运行依赖但没有锁文件：从 git 或 npm 安装时会用 npm install 解析最新版本')
  }
  if (!existsSync(path.join(root, 'README.md'))) warn('', '缺少 README.md')

  return done({
    id,
    name,
    version,
    displayName,
    description,
    platform,
    extensions,
    skills,
    events,
    permissions,
    actions: [...actionIds],
    schedules: scheduleIds,
    channels: channelIds,
    identityChannels,
    http,
    mcpServers: Object.keys(mcpServers).sort(),
    bin,
  })
}

function checkPath(root: string, rel: string, field: string, allowDir: boolean, error: (p: string, m: string) => void): boolean {
  const clean = path.normalize(rel)
  if (rel === '' || path.isAbsolute(rel) || clean === '..' || clean.startsWith(`..${path.sep}`)) {
    error(field, `${JSON.stringify(rel)} 须是插件目录内的相对路径`)
    return false
  }
  const full = path.join(root, clean)
  if (!existsSync(full)) {
    error(field, `${JSON.stringify(rel)} 不存在`)
    return false
  }
  if (!allowDir && statSync(full).isDirectory()) {
    error(field, `${JSON.stringify(rel)} 须是文件`)
    return false
  }
  return true
}

// checkSchema does structural checks: the shapes of type, properties, required, items and enum, and that x-secret and x-scope are used
// only where they take effect: top says which of them takes effect on this schema's top-level fields (the server honors x-secret at
// the top level of config and account fields, and x-scope only at the top level of config); anywhere else gets a warning.
function checkSchema(
  schema: Json,
  at: string,
  top: { secret: boolean; scope: boolean },
  error: (p: string, m: string) => void,
  warn: (p: string, m: string) => void,
): void {
  const walk = (s: unknown, p: string, depth: number) => {
    if (typeof s === 'boolean') return
    if (!isObject(s)) {
      error(p, 'JSON Schema 须是对象')
      return
    }
    const t = s.type
    if (t !== undefined) {
      const types = Array.isArray(t) ? t : [t]
      if (!types.every((x) => typeof x === 'string' && SCHEMA_TYPES.includes(x))) error(`${p}.type`, `type 只能是 ${SCHEMA_TYPES.join('、')}`)
    }
    if (s.enum !== undefined && !Array.isArray(s.enum)) error(`${p}.enum`, 'enum 须是数组')
    if (s.required !== undefined && !strings(s.required)) error(`${p}.required`, 'required 须是字符串数组')
    if (s.items !== undefined) walk(s.items, `${p}.items`, depth + 1)
    if (s.properties !== undefined) {
      if (!isObject(s.properties)) error(`${p}.properties`, 'properties 须是对象')
      else {
        for (const [k, v] of Object.entries(s.properties)) {
          const fp = `${p}.properties.${k}`
          walk(v, fp, depth + 1)
          if (!isObject(v)) continue
          if (v['x-secret'] !== undefined) {
            if (typeof v['x-secret'] !== 'boolean') error(`${fp}.x-secret`, 'x-secret 须是布尔值')
            else if (depth > 0 || !top.secret) warn(`${fp}.x-secret`, 'x-secret 只对配置与账号字段的顶层生效')
            else if (v['x-secret'] && v.type !== undefined && v.type !== 'string') error(`${fp}.x-secret`, 'x-secret 字段的 type 须为 string')
          }
          if (v['x-scope'] !== undefined) {
            if (v['x-scope'] !== 'project') error(`${fp}.x-scope`, 'x-scope 只能是 "project"')
            else if (depth > 0 || !top.scope) warn(`${fp}.x-scope`, 'x-scope 只对配置的顶层字段生效')
          }
        }
        if (strings(s.required)) {
          for (const r of s.required) if (!Object.hasOwn(s.properties, r)) warn(`${p}.required`, `required 里的 ${JSON.stringify(r)} 不在 properties 中`)
        }
      }
    }
  }
  walk(schema, at, 0)
}

function releaseParts(v: string): number[] | null {
  const out: number[] = []
  for (const f of v.split(/[.+-]/).filter(Boolean)) {
    if (!/^\d+$/.test(f)) break
    out.push(Number(f))
  }
  return out.length >= 2 ? out : null
}

// versionAtLeast compares release versions in YYYY.MM form; a platform version not in that form (a dev build) counts as satisfying it.
export function versionAtLeast(platform: string, required: string): boolean {
  const have = releaseParts(platform)
  if (have === null) return true
  const want = releaseParts(required)
  if (want === null) return false
  for (let i = 0; i < Math.max(have.length, want.length); i++) {
    const h = have[i] ?? 0
    const w = want[i] ?? 0
    if (h !== w) return h > w
  }
  return true
}
