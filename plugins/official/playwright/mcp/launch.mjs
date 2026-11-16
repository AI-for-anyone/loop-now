// MCP server entry: checks the prepared dedicated Chromium, then starts the pinned version of the official Playwright MCP (stdio)
// in this process. Nothing is downloaded and npx is never called at run time. The browser lives in the plugin data directory,
// prepared beforehand by setup.mjs; profiles are kept separately per Agent.
//
// Usage (the platform writes this into the Agent's mcp.json): node launch.mjs --data <plugin data dir> --agent <Agent id>
// [-- <extra args passed to the MCP>]

import { createHash } from 'node:crypto'
import { existsSync, mkdirSync, readFileSync, readlinkSync, statSync } from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)))
const MCP_VERSION = '0.0.83'

function fail(message) {
  process.stderr.write(`[playwright] ${message}\n`)
  process.exit(1)
}

function parse(argv) {
  const out = { data: '', agent: '', extra: [] }
  for (let i = 0; i < argv.length; i++) {
    if (argv[i] === '--') {
      out.extra = argv.slice(i + 1)
      break
    }
    if (argv[i] === '--data') out.data = argv[++i] ?? ''
    else if (argv[i] === '--agent') out.agent = argv[++i] ?? ''
    else fail(`Unknown argument ${argv[i]}`)
  }
  return out
}

// Chromium locks a profile directory with SingletonLock (a symlink pointing to "<hostname>-<pid>"); while the process holding it is
// still alive, the directory is in use by another browser.
function profileInUse(dir) {
  try {
    const target = readlinkSync(path.join(dir, 'SingletonLock'))
    const pid = Number(target.slice(target.lastIndexOf('-') + 1))
    if (!Number.isInteger(pid) || pid <= 0) return false
    process.kill(pid, 0)
    return true
  } catch (err) {
    return err?.code === 'EPERM'
  }
}

const args = parse(process.argv.slice(2))
if (!path.isAbsolute(args.data)) fail('--data must be the absolute path of the plugin data directory')
if (!/^[A-Za-z0-9-]{1,64}$/.test(args.agent)) fail('--agent must be an Agent id')

const require = createRequire(import.meta.url)
let mcpPkg
try {
  mcpPkg = require('@playwright/mcp/package.json')
} catch {
  fail(`@playwright/mcp is not installed in the plugin directory: run npm ci --ignore-scripts --omit=dev in ${root}`)
}
if (mcpPkg.version !== MCP_VERSION) fail(`Needs @playwright/mcp ${MCP_VERSION}, but ${mcpPkg.version} is installed`)

const browserDir = path.join(args.data, 'browser')
const manifestPath = path.join(browserDir, 'browser.json')
if (!existsSync(manifestPath)) {
  fail(`The dedicated browser is not prepared yet: on the server's machine, run node ${path.join(root, 'setup.mjs')} ${args.data}`)
}
const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))
const platform = `${process.platform}-${process.arch}`
if (manifest.platform !== platform) fail(`The prepared browser is for ${manifest.platform}, but this machine is ${platform}; run setup.mjs again`)
if (manifest.mcpVersion !== MCP_VERSION) fail(`The browser was prepared for MCP ${manifest.mcpVersion}, but ${MCP_VERSION} is needed; run setup.mjs again`)
const executable = path.resolve(browserDir, manifest.executable)
if (path.relative(browserDir, executable).startsWith('..') || !existsSync(executable) || !statSync(executable).isFile()) {
  fail('The prepared browser executable is missing; run setup.mjs again')
}
if (createHash('sha256').update(readFileSync(executable)).digest('hex') !== manifest.sha256) {
  fail('The prepared browser executable does not match its checksum; run setup.mjs again')
}

const profile = path.join(args.data, 'profiles', args.agent)
const output = path.join(args.data, 'output', args.agent)
mkdirSync(profile, { recursive: true, mode: 0o700 })
mkdirSync(output, { recursive: true, mode: 0o700 })
const mcpArgs = ['--executable-path', executable, '--output-dir', output, '--timeout-action', '10000', '--timeout-navigation', '45000']
if (profileInUse(profile)) {
  // Another run of the same Agent (e.g. a copy in a task) has this profile directory open: this time use an in-memory profile,
  // discarded at the end.
  process.stderr.write(`[playwright] Profile ${profile} is in use by another browser; using a temporary profile this time\n`)
  mcpArgs.push('--isolated')
} else {
  mcpArgs.push('--user-data-dir', profile)
}
mcpArgs.push(...args.extra)

// The package's exports do not export cli.js: find it in the package directory via the bin in package.json.
const cli = path.join(path.dirname(require.resolve('@playwright/mcp/package.json')), mcpPkg.bin['playwright-mcp'])
process.argv = [process.argv[0], cli, ...mcpArgs]
await import(pathToFileURL(cli).href)
