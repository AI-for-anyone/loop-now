// Prepares the dedicated Chromium once on the machine running the server: downloads it under browser/ in the plugin data directory
// and records the executable and its checksum. Only this step goes online; at run time (mcp/launch.mjs) nothing is downloaded.
// Run it with the platform's Node 24:
//
//   npm ci --prefix <plugin dir> --ignore-scripts --omit=dev --no-audit --no-fund   (already installed for official plugins shipped with the platform)
//   node <plugin dir>/setup.mjs <work root>/platform/plugins/playwright/data

import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const MCP_VERSION = '0.0.83'
const data = process.argv[2]
if (!data || !path.isAbsolute(data)) {
  process.stderr.write('Usage: node setup.mjs <absolute path of the plugin data directory>, e.g. <work root>/platform/plugins/playwright/data\n')
  process.exit(2)
}
const require = createRequire(import.meta.url)
const mcp = require('@playwright/mcp/package.json')
if (mcp.version !== MCP_VERSION) throw new Error(`Needs @playwright/mcp ${MCP_VERSION}, but ${mcp.version} is installed`)
const playwrightDir = path.dirname(require.resolve('playwright/package.json', { paths: [path.dirname(require.resolve('@playwright/mcp/package.json'))] }))
const playwright = JSON.parse(readFileSync(path.join(playwrightDir, 'package.json'), 'utf8'))

const browserDir = path.join(data, 'browser')
mkdirSync(browserDir, { recursive: true, mode: 0o700 })
// The download uses only this directory and never reads or writes the current user's browser cache or profile.
const env = { PATH: `${path.dirname(process.execPath)}:/usr/bin:/bin`, HOME: browserDir, PLAYWRIGHT_BROWSERS_PATH: browserDir }
const result = spawnSync(process.execPath, [path.join(playwrightDir, 'cli.js'), 'install', 'chromium', '--no-shell'], { stdio: 'inherit', env })
if (result.status !== 0) throw new Error('Failed to download the dedicated Chromium')

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
process.stdout.write(`Dedicated Chromium is ready: ${executable}\n`)
