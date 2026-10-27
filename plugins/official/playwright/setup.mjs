// 在服务端所在机器上准备一次专用 Chromium：下载到插件数据目录的 browser/ 下，记录可执行文件与校验值。
// 只有这一步联网；运行时（mcp/launch.mjs）不下载任何东西。用平台的 Node 24 运行：
//
//   npm ci --prefix <插件目录> --ignore-scripts --omit=dev --no-audit --no-fund   （官方插件随平台发布时已装好）
//   node <插件目录>/setup.mjs <work root>/platform/plugins/playwright/data

import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const MCP_VERSION = '0.0.83'
const data = process.argv[2]
if (!data || !path.isAbsolute(data)) {
  process.stderr.write('用法：node setup.mjs <插件数据目录的绝对路径>，例如 <work root>/platform/plugins/playwright/data\n')
  process.exit(2)
}
const require = createRequire(import.meta.url)
const mcp = require('@playwright/mcp/package.json')
if (mcp.version !== MCP_VERSION) throw new Error(`需要 @playwright/mcp ${MCP_VERSION}，装的是 ${mcp.version}`)
const playwrightDir = path.dirname(require.resolve('playwright/package.json', { paths: [path.dirname(require.resolve('@playwright/mcp/package.json'))] }))
const playwright = JSON.parse(readFileSync(path.join(playwrightDir, 'package.json'), 'utf8'))

const browserDir = path.join(data, 'browser')
mkdirSync(browserDir, { recursive: true, mode: 0o700 })
// 下载只用这个目录，不读写当前用户的浏览器缓存或配置。
const env = { PATH: `${path.dirname(process.execPath)}:/usr/bin:/bin`, HOME: browserDir, PLAYWRIGHT_BROWSERS_PATH: browserDir }
const result = spawnSync(process.execPath, [path.join(playwrightDir, 'cli.js'), 'install', 'chromium', '--no-shell'], { stdio: 'inherit', env })
if (result.status !== 0) throw new Error('下载专用 Chromium 失败')

process.env.PLAYWRIGHT_BROWSERS_PATH = browserDir
const { chromium } = await import(pathToFileURL(path.join(playwrightDir, 'index.mjs')).href)
const executable = chromium.executablePath()
const manifest = {
  platform: `${process.platform}-${process.arch}`,
  executable: path.relative(browserDir, executable),
  sha256: createHash('sha256').update(readFileSync(executable)).digest('hex'),
  mcpVersion: mcp.version,
  playwrightVersion: playwright.version,
  preparedAt: new Date().toISOString(),
}
writeFileSync(path.join(browserDir, 'browser.json'), `${JSON.stringify(manifest, null, 2)}\n`, { mode: 0o600 })
process.stdout.write(`专用 Chromium 已准备：${executable}\n`)
