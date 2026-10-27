// MCP 服务器入口：核对已准备好的专用 Chromium，再在本进程里启动固定版本的官方 Playwright MCP（stdio）。
// 运行时不下载、不调用 npx。浏览器在插件数据目录里，由 setup.mjs 预先准备；配置文件按 Agent 分开。
//
// 用法（平台写进 Agent 的 mcp.json）：node launch.mjs --data <插件数据目录> --agent <Agent id> [-- <传给 MCP 的额外参数>]

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
    else fail(`未知的参数 ${argv[i]}`)
  }
  return out
}

// Chromium 用 SingletonLock（指向 "<主机名>-<pid>" 的符号链接）独占配置目录；持有它的进程还在时目录正被别的浏览器使用。
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
if (!path.isAbsolute(args.data)) fail('--data 须是插件数据目录的绝对路径')
if (!/^[A-Za-z0-9-]{1,64}$/.test(args.agent)) fail('--agent 须是 Agent id')

const require = createRequire(import.meta.url)
let mcpPkg
try {
  mcpPkg = require('@playwright/mcp/package.json')
} catch {
  fail(`插件目录里没有安装 @playwright/mcp：在 ${root} 运行 npm ci --ignore-scripts --omit=dev`)
}
if (mcpPkg.version !== MCP_VERSION) fail(`需要 @playwright/mcp ${MCP_VERSION}，装的是 ${mcpPkg.version}`)

const browserDir = path.join(args.data, 'browser')
const manifestPath = path.join(browserDir, 'browser.json')
if (!existsSync(manifestPath)) {
  fail(`专用浏览器还没有准备：在服务端所在机器上运行 node ${path.join(root, 'setup.mjs')} ${args.data}`)
}
const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'))
const platform = `${process.platform}-${process.arch}`
if (manifest.platform !== platform) fail(`准备的浏览器是 ${manifest.platform} 的，这台机器是 ${platform}；请重新运行 setup.mjs`)
if (manifest.mcpVersion !== MCP_VERSION) fail(`准备浏览器时的 MCP 版本是 ${manifest.mcpVersion}，需要 ${MCP_VERSION}；请重新运行 setup.mjs`)
const executable = path.resolve(browserDir, manifest.executable)
if (path.relative(browserDir, executable).startsWith('..') || !existsSync(executable) || !statSync(executable).isFile()) {
  fail('准备的浏览器可执行文件不在了；请重新运行 setup.mjs')
}
if (createHash('sha256').update(readFileSync(executable)).digest('hex') !== manifest.sha256) {
  fail('准备的浏览器可执行文件校验不符；请重新运行 setup.mjs')
}

const profile = path.join(args.data, 'profiles', args.agent)
const output = path.join(args.data, 'output', args.agent)
mkdirSync(profile, { recursive: true, mode: 0o700 })
mkdirSync(output, { recursive: true, mode: 0o700 })
const mcpArgs = ['--executable-path', executable, '--output-dir', output, '--timeout-action', '10000', '--timeout-navigation', '45000']
if (profileInUse(profile)) {
  // 同一 Agent 的另一个执行（例如任务里的副本）正开着这个配置目录：这次用只在内存里的配置，结束即丢弃。
  process.stderr.write(`[playwright] 配置目录 ${profile} 正被另一个浏览器使用，这次使用临时配置\n`)
  mcpArgs.push('--isolated')
} else {
  mcpArgs.push('--user-data-dir', profile)
}
mcpArgs.push(...args.extra)

// 包的 exports 没有导出 cli.js：按 package.json 的 bin 在包目录里找。
const cli = path.join(path.dirname(require.resolve('@playwright/mcp/package.json')), mcpPkg.bin['playwright-mcp'])
process.argv = [process.argv[0], cli, ...mcpArgs]
await import(pathToFileURL(cli).href)
