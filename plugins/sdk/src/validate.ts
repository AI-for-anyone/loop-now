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
const LOCALIZED_RULE = 'must be a string, or an object with language tags (such as zh-CN, en-US) as keys and non-empty strings as values, with at least one entry.'
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

// Writes a list the way the server's messages do: "a, b or c".
function orList(items: readonly string[]): string {
  return items.length < 2 ? items.join('') : `${items.slice(0, -1).join(', ')} or ${items[items.length - 1]}`
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
  if (isLocalizedObject(v)) return v
  error(field, `${field} ${LOCALIZED_RULE}`)
  return fallback
}

/** Whether v is a valid per-language object for localized text (the object form that localizedField accepts). */
function isLocalizedObject(v: unknown): v is Record<string, string> {
  return isObject(v) && Object.keys(v).length > 0 && Object.entries(v).every(([k, t]) => LANGUAGE_TAG.test(k) && typeof t === 'string' && t.trim() !== '')
}

/** A label (actions, channels, account kinds): a non-empty string or a per-language object. */
function isLabel(v: unknown): boolean {
  return (typeof v === 'string' && v !== '') || isLocalizedObject(v)
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
    error('', `${root} is not a directory.`)
    return done(null)
  }
  const pkgPath = path.join(root, 'package.json')
  if (!existsSync(pkgPath)) {
    error('', 'The plugin folder has no package.json')
    return done(null)
  }
  let pkg: unknown
  try {
    pkg = JSON.parse(readFileSync(pkgPath, 'utf8'))
  } catch (err) {
    error('', `package.json is not valid JSON: ${(err as Error).message}`)
    return done(null)
  }
  if (!isObject(pkg)) {
    error('', 'package.json must be an object.')
    return done(null)
  }
  const pi = pkg.pi
  const ln = pkg.loopNow
  if (pi === undefined && ln === undefined) {
    error('', 'package.json has neither a pi section nor a loopNow section, so it is not a plugin.')
    return done(null)
  }
  if (pi !== undefined && !isObject(pi)) error('pi', 'pi must be an object.')
  if (ln !== undefined && !isObject(ln)) error('loopNow', 'loopNow must be an object.')
  const piObj = isObject(pi) ? pi : {}
  const lnObj = isObject(ln) ? ln : {}

  const name = typeof pkg.name === 'string' ? pkg.name : ''
  const version = typeof pkg.version === 'string' ? pkg.version : ''
  const description = localizedField(pkg.description, 'description', '', error)
  if (!VERSION_PATTERN.test(version)) error('version', 'version must be 1 to 64 letters, digits, dots, plus signs or hyphens.')

  const list = (obj: Json, key: string, field: string): string[] => {
    const v = obj[key]
    if (v === undefined) return []
    if (!strings(v)) {
      error(field, `${field} must be an array of strings.`)
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
  if (lnObj.id !== undefined && typeof lnObj.id !== 'string') error('loopNow.id', 'loopNow.id must be a string.')
  if (id === '') {
    // A standard Pi package with only a pi section: use the npm name without its scope as the id (same as the server).
    id = (name.split('/').pop() ?? '')
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 64)
      .replace(/-+$/, '')
    if (ln !== undefined) warn('loopNow.id', `No loopNow.id is declared, so the id ${JSON.stringify(id)} is derived from the npm name. Declare it explicitly, because it cannot be changed after installing.`)
  }
  if (!ID_PATTERN.test(id)) error('loopNow.id', 'loopNow.id must be 1 to 64 lowercase letters, digits or hyphens, and must not start or end with a hyphen.')
  const declaredName = localizedField(lnObj.displayName, 'loopNow.displayName', '', error)
  const displayName = declaredName === '' ? name || id : declaredName

  const minPlatform = lnObj.minPlatformVersion
  if (minPlatform !== undefined && minPlatform !== null) {
    if (typeof minPlatform !== 'string') error('loopNow.minPlatformVersion', 'loopNow.minPlatformVersion must be a string.')
    else if (releaseParts(minPlatform) === null) error('loopNow.minPlatformVersion', 'loopNow.minPlatformVersion must be a release version of the form YYYY.MM.')
    else if (options.platformVersion && !versionAtLeast(options.platformVersion, minPlatform)) {
      error('loopNow.minPlatformVersion', `The plugin requires platform version ${minPlatform} or later; the current version is ${options.platformVersion}.`)
    }
  }

  const platform = typeof lnObj.platform === 'string' && lnObj.platform !== '' ? lnObj.platform : null
  if (lnObj.platform !== undefined && typeof lnObj.platform !== 'string') error('loopNow.platform', 'loopNow.platform must be a string.')
  const mcpServers = isObject(lnObj.mcpServers) ? lnObj.mcpServers : {}
  if (lnObj.mcpServers !== undefined && !isObject(lnObj.mcpServers)) error('loopNow.mcpServers', 'loopNow.mcpServers must be an object.')

  if (extensions.length === 0 && skills.length === 0 && platform === null && Object.keys(mcpServers).length === 0 && bin.length === 0) {
    error('', 'The plugin has neither an Agent side (extensions, Skills, MCP servers, CLIs) nor a platform side.')
  }

  const seen = new Set<string>()
  events.forEach((e, i) => {
    if (!Object.hasOwn(EVENTS, e)) error(`loopNow.events[${i}]`, `${JSON.stringify(e)} is not a platform event.`)
    else if (seen.has(e)) error(`loopNow.events[${i}]`, `${JSON.stringify(e)} appears more than once.`)
    seen.add(e)
  })
  permissions.forEach((p, i) => {
    if (!(PERMISSIONS as readonly string[]).includes(p)) error(`loopNow.permissions[${i}]`, `${JSON.stringify(p)} is not an available scope.`)
  })
  if (lnObj.failClosed !== undefined && typeof lnObj.failClosed !== 'boolean') error('loopNow.failClosed', 'loopNow.failClosed must be a boolean.')
  if (lnObj.failClosed === true && !events.some((e) => (EVENTS as Record<string, string>)[e] === 'intercept')) {
    warn('loopNow.failClosed', 'failClosed only affects intercept events, but the manifest subscribes to no intercept event.')
  }

  const actions = Array.isArray(lnObj.actions) ? lnObj.actions : []
  if (lnObj.actions !== undefined && !Array.isArray(lnObj.actions)) error('loopNow.actions', 'loopNow.actions must be an array.')
  const schedules = Array.isArray(lnObj.schedule) ? lnObj.schedule : []
  if (lnObj.schedule !== undefined && !Array.isArray(lnObj.schedule)) error('loopNow.schedule', 'loopNow.schedule must be an array.')
  const channels = Array.isArray(lnObj.channels) ? lnObj.channels : []
  if (lnObj.channels !== undefined && !Array.isArray(lnObj.channels)) error('loopNow.channels', 'loopNow.channels must be an array.')
  const http = lnObj.http === true
  if (lnObj.http !== undefined && typeof lnObj.http !== 'boolean') error('loopNow.http', 'loopNow.http must be a boolean.')

  if ((events.length > 0 || actions.length > 0 || channels.length > 0 || http || schedules.length > 0) && platform === null) {
    error('loopNow.platform', 'Event subscriptions, action buttons, channels, HTTP and schedules are handled by the platform side, so loopNow.platform must be declared.')
  }
  if (platform !== null && checkPath(root, platform, 'loopNow.platform', false, error)) {
    if (!ENTRY_EXTENSIONS.includes(path.extname(platform))) warn('loopNow.platform', `The platform side entry usually ends in ${orList(ENTRY_EXTENSIONS)}.`)
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
        ? `${name} uses SSE, which is not supported. Use a streamable HTTP url instead.`
        : problem === 'type'
          ? `${name} must have type stdio, http or streamable-http.`
          : `${name} must be a stdio entry with a command or a streamable HTTP entry with an http or https url, and its name must use only lowercase letters, digits, underscores or hyphens.`,
    )
  }

  if (lnObj.configSchema !== undefined) {
    if (!isObject(lnObj.configSchema)) error('loopNow.configSchema', 'loopNow.configSchema must be an object.')
    else {
      if (lnObj.configSchema.type !== undefined && lnObj.configSchema.type !== 'object') error('loopNow.configSchema.type', 'loopNow.configSchema must have type object.')
      checkSchema(lnObj.configSchema, 'loopNow.configSchema', { secret: true, scope: true }, error, warn)
    }
  }

  const accounts = Array.isArray(lnObj.accounts) ? lnObj.accounts : []
  if (lnObj.accounts !== undefined && !Array.isArray(lnObj.accounts)) error('loopNow.accounts', 'loopNow.accounts must be an array.')
  const kinds = new Set<string>()
  accounts.forEach((a: unknown, i) => {
    const p = `loopNow.accounts[${i}]`
    if (!isObject(a) || typeof a.kind !== 'string' || !KIND_PATTERN.test(a.kind) || kinds.has(a.kind)) {
      error(p, 'Each kind in loopNow.accounts must be unique, use only lowercase letters, digits or underscores, and start with a letter.')
      return
    }
    kinds.add(a.kind)
    if (a.label !== undefined && !isLabel(a.label)) error(`${p}.label`, `${p}.label ${LOCALIZED_RULE}`)
    if (!isObject(a.fields)) error(`${p}.fields`, `loopNow.accounts entry ${a.kind} has no fields.`)
    else checkSchema(a.fields, `${p}.fields`, { secret: true, scope: false }, error, warn)
  })

  const actionIds = new Set<string>()
  actions.forEach((a: unknown, i) => {
    const p = `loopNow.actions[${i}]`
    if (!isObject(a) || typeof a.id !== 'string' || !SLUG_PATTERN.test(a.id) || actionIds.has(a.id) || a.label === undefined || a.label === '' || !strings(a.where) || a.where.length === 0) {
      error(p, 'Each entry in loopNow.actions must have a unique id, a label and where.')
      return
    }
    actionIds.add(a.id)
    if (!isLabel(a.label)) error(`${p}.label`, `${p}.label ${LOCALIZED_RULE}`)
    if (a.where.some((w) => !WHERE.includes(w))) error(`${p}.where`, `In loopNow.actions, where can only contain ${orList(WHERE)}.`)
    if (a.fields !== undefined) {
      if (!isObject(a.fields)) error(`${p}.fields`, `loopNow.actions entry ${a.id} has fields that are not an object.`)
      else checkSchema(a.fields, `${p}.fields`, { secret: false, scope: false }, error, warn)
    }
  })
  const channelIds: string[] = []
  const identityChannels: string[] = []
  channels.forEach((c: unknown, i) => {
    if (!isObject(c) || typeof c.id !== 'string' || !SLUG_PATTERN.test(c.id) || c.label === undefined || c.label === '') {
      error(`loopNow.channels[${i}]`, 'Each entry in loopNow.channels must have an id and a label.')
      return
    }
    channelIds.push(c.id)
    if (!isLabel(c.label)) error(`loopNow.channels[${i}].label`, `loopNow.channels[${i}].label ${LOCALIZED_RULE}`)
    if (c.manageIdentities !== undefined && typeof c.manageIdentities !== 'boolean') error(`loopNow.channels[${i}].manageIdentities`, 'manageIdentities must be a boolean.')
    if (c.manageIdentities === true) {
      identityChannels.push(c.id)
      if (!permissions.includes('identities.manage')) error(`loopNow.channels[${i}].manageIdentities`, 'This channel manages external identities, so identities.manage must be declared in loopNow.permissions.')
    }
  })
  const scheduleIds: string[] = []
  schedules.forEach((s: unknown, i) => {
    if (!isObject(s) || typeof s.id !== 'string' || !SLUG_PATTERN.test(s.id) || typeof s.cron !== 'string' || s.cron.trim().split(/\s+/).length !== 5) {
      error(`loopNow.schedule[${i}]`, 'Each entry in loopNow.schedule must have an id and a cron expression with five fields.')
      return
    }
    scheduleIds.push(s.id)
  })

  // The following are warnings: they do not block installing.
  const keywords = strings(pkg.keywords) ? pkg.keywords : []
  if (ln !== undefined && !keywords.includes('loop-now-plugin')) warn('keywords', 'Add loop-now-plugin to keywords so that the plugin can be found in the marketplace and on npm.')
  if (extensions.length > 0 && !keywords.includes('pi-package')) warn('keywords', 'A plugin with Pi extensions should add the pi-package keyword (the Pi package convention).')
  if (description === '') warn('description', 'description is missing, so the install confirmation and the plugin page show no description.')
  if (ln !== undefined && (lnObj.displayName === undefined || lnObj.displayName === '')) warn('loopNow.displayName', `loopNow.displayName is missing, so ${JSON.stringify(displayName)} is shown as the name.`)
  const deps = isObject(pkg.dependencies) ? pkg.dependencies : {}
  for (const dep of HOST_PROVIDED) {
    if (Object.hasOwn(deps, dep)) warn(`dependencies.${dep}`, `${dep} is provided by the host or Pi. Put it in peerDependencies ("*") instead of installing it with the plugin.`)
  }
  if (Object.keys(deps).length > 0 && !existsSync(path.join(root, 'package-lock.json')) && !existsSync(path.join(root, 'npm-shrinkwrap.json'))) {
    warn('dependencies', 'There are runtime dependencies but no lock file: installing from git or npm resolves the latest versions with npm install.')
  }
  if (!existsSync(path.join(root, 'README.md'))) warn('', 'README.md is missing.')

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
    error(field, `${JSON.stringify(rel)} must be a relative path inside the plugin folder.`)
    return false
  }
  const full = path.join(root, clean)
  if (!existsSync(full)) {
    error(field, `${JSON.stringify(rel)} does not exist.`)
    return false
  }
  if (!allowDir && statSync(full).isDirectory()) {
    error(field, `${JSON.stringify(rel)} must be a file.`)
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
      error(p, 'The JSON Schema must be an object.')
      return
    }
    const t = s.type
    if (t !== undefined) {
      const types = Array.isArray(t) ? t : [t]
      if (!types.every((x) => typeof x === 'string' && SCHEMA_TYPES.includes(x))) error(`${p}.type`, `type must be ${orList(SCHEMA_TYPES)}.`)
    }
    if (s.enum !== undefined && !Array.isArray(s.enum)) error(`${p}.enum`, 'enum must be an array.')
    if (Array.isArray(s.oneOf)) s.oneOf.forEach((option, i) => walk(option, `${p}.oneOf[${i}]`, depth + 1))
    if (s.required !== undefined && !strings(s.required)) error(`${p}.required`, 'required must be an array of strings.')
    if (s.items !== undefined) walk(s.items, `${p}.items`, depth + 1)
    if (s.properties !== undefined) {
      if (!isObject(s.properties)) error(`${p}.properties`, 'properties must be an object.')
      else {
        for (const [k, v] of Object.entries(s.properties)) {
          const fp = `${p}.properties.${k}`
          walk(v, fp, depth + 1)
          if (!isObject(v)) continue
          if (v['x-secret'] !== undefined) {
            if (typeof v['x-secret'] !== 'boolean') error(`${fp}.x-secret`, 'x-secret must be a boolean.')
            else if (depth > 0 || !top.secret) warn(`${fp}.x-secret`, 'x-secret only takes effect on top-level config and account fields.')
            else if (v['x-secret'] && v.type !== undefined && v.type !== 'string') error(`${fp}.x-secret`, 'An x-secret field must have type string.')
          }
          if (v['x-scope'] !== undefined) {
            if (v['x-scope'] !== 'project') error(`${fp}.x-scope`, 'x-scope must be "project".')
            else if (depth > 0 || !top.scope) warn(`${fp}.x-scope`, 'x-scope only takes effect on top-level config fields.')
          }
        }
        if (strings(s.required)) {
          for (const r of s.required) if (!Object.hasOwn(s.properties, r)) warn(`${p}.required`, `required lists ${JSON.stringify(r)}, which is not in properties.`)
        }
      }
    }
  }
  walk(schema, at, 0)
  checkLocalizedText(schema, at, error)
}

// Keywords whose value holds subschemas, the same list the server walks: a map of name to schema, an array of schemas, or one schema.
// (dependencies may also map a name to an array of property names; such values are not schemas and are skipped.)
const SCHEMA_MAPS = ['properties', 'patternProperties', '$defs', 'definitions', 'dependentSchemas', 'dependencies'] as const
const SCHEMA_ARRAYS = ['prefixItems', 'allOf', 'anyOf', 'oneOf'] as const
const SCHEMA_SINGLES = ['not', 'if', 'then', 'else', 'additionalProperties', 'contains', 'propertyNames', 'unevaluatedItems', 'unevaluatedProperties', 'contentSchema'] as const

// checkLocalizedText reports a title or description that is neither a string nor valid LocalizedText, in every subschema. The server
// reports an invalid object with the same sentence and leaves other values (a number, a boolean, an array, null) to the JSON Schema
// meta-schema, which rejects them at install time; here they get the same sentence, so a plugin author sees them when running
// validate.
function checkLocalizedText(s: unknown, p: string, error: (p: string, m: string) => void): void {
  if (!isObject(s)) return
  for (const key of ['title', 'description'] as const) {
    if (s[key] !== undefined && typeof s[key] !== 'string' && !isLocalizedObject(s[key])) error(`${p}.${key}`, `${p}.${key} ${LOCALIZED_RULE}`)
  }
  for (const key of SCHEMA_MAPS) {
    const map = s[key]
    if (isObject(map)) for (const [name, sub] of Object.entries(map)) checkLocalizedText(sub, `${p}.${key}.${name}`, error)
  }
  for (const key of SCHEMA_ARRAYS) {
    const list = s[key]
    if (Array.isArray(list)) list.forEach((sub, i) => checkLocalizedText(sub, `${p}.${key}[${i}]`, error))
  }
  if (Array.isArray(s.items)) s.items.forEach((sub, i) => checkLocalizedText(sub, `${p}.items[${i}]`, error))
  else checkLocalizedText(s.items, `${p}.items`, error)
  for (const key of SCHEMA_SINGLES) checkLocalizedText(s[key], `${p}.${key}`, error)
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
