/* global document, window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'

const manifest = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_DB_MANIFEST, 'utf8'))
const api = new URL(manifest.apiBase), web = new URL(manifest.webOrigin)
assert.equal(api.hostname, '127.0.0.1'); assert.equal(api.protocol, 'http:'); assert.equal(api.pathname, '/api/v1')
assert.ok(api.port && !['8780', '8781', '5173'].includes(api.port))
assert.equal(web.origin, 'http://127.0.0.1:4318')
assert.ok(!api.username && !api.password && !api.search && !api.hash)
const out = new URL('../test-results/add-cloud-database-cli-templates/app-db/', import.meta.url)
mkdirSync(out, { recursive: true })
const checks = [], errors = [], denied = [], ids = {}, screenshots = []
let browser, preview, forwarded = []
const root = fileURLToPath(new URL('../', import.meta.url))
const startPreview = () => new Promise((resolve, reject) => {
  preview = spawn(process.execPath, [root + 'node_modules/vite/bin/vite.js', '--host', '127.0.0.1', '--port', web.port, '--strictPort'], { cwd: root, env: { PATH: process.env.PATH }, stdio: ['ignore', 'pipe', 'pipe'] })
  const timeout = setTimeout(() => reject(new Error('temporary frontend readiness timeout')), 10000)
  preview.stdout.on('data', buffer => { if (buffer.toString().includes(web.origin)) { clearTimeout(timeout); resolve() } })
  preview.once('exit', () => { clearTimeout(timeout); reject(new Error('temporary frontend exited')) })
  preview.once('error', () => { clearTimeout(timeout); reject(new Error('temporary frontend spawn failed')) })
})
const ok = message => checks.push(message)
const until = async (read, condition) => {
  for (let index = 0; index < 150; index++) { const value = await read(); if (condition(value)) return value; await new Promise(resolve => setTimeout(resolve, 40)) }
  throw new Error('bounded actual state wait failed')
}
try {
  await startPreview(); browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  const page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  forwarded = await forwardTemporaryAPI(page, api.href.replace(/\/$/, ''), web.origin)
  const writeAllowed = path => /\/(credential-accounts(?:\/[^/]+(?:\/credential)?)?|db-connections\/(psql|mysql))$/.test(path)
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url())
    if (![web.origin, api.origin, 'http://127.0.0.1:8780'].includes(url.origin) || (request.method() !== 'GET' && !writeAllowed(url.pathname))) {
      denied.push({ method: request.method(), path: url.pathname }); return route.abort()
    }
    return route.fallback()
  })
  const actual = async (method, path, data, status = 200) => {
    assert.ok(method === 'GET' || writeAllowed(path))
    const response = await context.request.fetch(api.href.replace(/\/$/, '') + path, { method, data, headers: { origin: web.origin }, maxRedirects: 0, maxRetries: 0 })
    assert.equal(response.status(), status, `${method} ${path}: status mismatch`)
    return response.json()
  }
  const base = `/p/${encodeURIComponent(manifest.projectName)}`, config = `${base}/agents/${manifest.agentId}/config`
  const dbPath = cli => `/agents/${manifest.agentId}/db-connections/${cli}`
  const getDB = cli => actual('GET', dbPath(cli))
  const area = cli => page.getByText(`${cli} 授权连接集合`, { exact: true }).locator('..')
  const checkbox = (cli, name) => area(cli).getByRole('checkbox', { name: new RegExp(name) })
  const create = async (cli, name) => {
    await page.goto(web.origin + base + '/settings')
    const region = page.getByRole('region', { name: 'CLI 账号', exact: true })
    await region.getByLabel('添加 CLI 账号 · 工具').selectOption(cli)
    await region.getByRole('button', { name: '按模板录入账号', exact: true }).click()
    await region.getByLabel('账号名称', { exact: true }).fill(name)
    await region.getByLabel('主机', { exact: false }).fill('127.0.0.1')
    assert.equal(await region.getByLabel('端口', { exact: false }).inputValue(), cli === 'psql' ? '5432' : '3306')
    await region.getByLabel('用户名', { exact: false }).fill('synthetic_reader')
    await region.getByLabel('密码', { exact: false }).fill('synthetic-db-only-not-real')
    await region.getByLabel('数据库', { exact: false }).fill('synthetic_business')
    await region.getByLabel('TLS 模式', { exact: false }).selectOption(cli === 'psql' ? 'disable' : 'DISABLED')
    const ca = region.getByLabel('CA 证书 PEM', { exact: false })
    assert.equal(await ca.evaluate(element => element.tagName), 'TEXTAREA')
    await region.getByRole('button', { name: '保存', exact: true }).click()
    await region.getByRole('heading', { name, exact: true }).waitFor()
    assert.equal(await region.getByLabel('密码', { exact: false }).count(), 0)
    const accounts = await actual('GET', `/projects/${manifest.projectId}/credential-accounts`)
    const account = accounts.accounts.find(item => item.name === name); assert.ok(account); return account
  }
  assert.equal((await getDB('psql')).status, 'not_configured')
  const a = await create('psql', '浏览器PG连接A'), b = await create('psql', '浏览器PG连接B'), c = await create('mysql', '浏览器MySQL连接C')
  ids.a = a.id; ids.b = b.id; ids.c = c.id
  await page.goto(web.origin + base + '/settings')
  const cliRegion = page.getByRole('region', { name: 'CLI 账号', exact: true })
  let bRow = cliRegion.getByRole('listitem').filter({ has: page.getByRole('heading', { name: b.name, exact: true }) })
  await bRow.getByRole('button', { name: '改名', exact: true }).click()
  const renamed = '浏览器PG连接B · 改名后仍按稳定ID取用'
  await bRow.getByLabel('账号名称', { exact: true }).fill(renamed)
  await bRow.getByRole('button', { name: '保存', exact: true }).click()
  await cliRegion.getByRole('heading', { name: renamed, exact: true }).waitFor()
  b.name = renamed
  bRow = cliRegion.getByRole('listitem').filter({ has: page.getByRole('heading', { name: renamed, exact: true }) })
  await bRow.getByRole('button', { name: '整体替换凭证', exact: true }).click()
  await bRow.getByLabel('主机', { exact: false }).fill('127.0.0.1')
  await bRow.getByLabel('用户名', { exact: false }).fill('synthetic_replacement_reader')
  await bRow.getByLabel('密码', { exact: false }).fill('synthetic-replacement-not-real')
  await bRow.getByLabel('数据库', { exact: false }).fill('synthetic_business')
  await bRow.getByLabel('TLS 模式', { exact: false }).selectOption('disable')
  await bRow.getByRole('button', { name: '保存', exact: true }).click()
  await bRow.getByLabel('密码', { exact: false }).waitFor({ state: 'hidden' })
  const updated = await actual('GET', `/projects/${manifest.projectId}/credential-accounts/${b.id}`)
  assert.equal(updated.name, renamed); assert.equal(updated.id, b.id); assert.notEqual(updated.currentVersionId, b.currentVersionId)
  assert.ok(!JSON.stringify(updated).includes('synthetic-replacement-not-real'))
  ok('actual browser rename and credential replacement keep account ID; new version metadata, secret input cleared')
  assert.equal((await getDB('psql')).status, 'selection_required')
  assert.equal((await getDB('mysql')).mode, 'legacy_single')
  assert.deepEqual((await getDB('mysql')).accountIds, [c.id])
  ok('actual browser creates psql A/B and mysql C using formal template defaults and masked fields; zero/one/two legacy resolutions')
  await page.goto(web.origin + config)
  await checkbox('psql', a.name).waitFor()
  assert.equal(await checkbox('psql', a.name).isChecked(), false)
  await checkbox('psql', a.name).check(); await checkbox('psql', b.name).check()
  await area('psql').getByRole('button', { name: '保存连接集合', exact: true }).click()
  await until(() => getDB('psql'), value => value.mode === 'explicit_set' && value.accountIds.length === 2)
  await page.getByLabel('psql 项目账号', { exact: true }).waitFor({ state: 'hidden' })
  ok('actual React PUT A+B switches mode and disables legacy single editor; stable IDs')

  await checkbox('psql', b.name).uncheck()
  let inject = true
  await page.route('**/db-connections/psql', async route => {
    if (route.request().method() === 'PUT' && inject) {
      inject = false; const old = await getDB('psql')
      await actual('PUT', dbPath('psql'), { accountIds: [b.id], expectedConfigVersion: old.configVersion })
    }
    return route.fallback()
  })
  const before = forwarded.filter(item => item.method === 'PUT' && item.path.endsWith('/db-connections/psql')).length
  await area('psql').getByRole('button', { name: '保存连接集合', exact: true }).click()
  await area('psql').getByText('配置已在别处更新，尚未保存本次修改').waitFor()
  assert.equal(await checkbox('psql', a.name).isChecked(), true)
  assert.equal(await checkbox('psql', b.name).isChecked(), false)
  assert.equal(forwarded.filter(item => item.method === 'PUT' && item.path.endsWith('/db-connections/psql')).length, before + 1)
  await area('psql').getByRole('button', { name: '已核对，采用此版本', exact: true }).click()
  await area('psql').getByRole('button', { name: '保存连接集合', exact: true }).click()
  await until(() => getDB('psql'), value => value.accountIds.length === 1 && value.accountIds[0] === a.id)
  ok('actual concurrent App write causes 412; one browser PUT, local A draft survives, explicit review retries latest CAS')

  await checkbox('psql', a.name).uncheck()
  await area('psql').getByRole('button', { name: '保存空集合（不授权连接）', exact: true }).click()
  await until(() => getDB('psql'), value => value.mode === 'explicit_set' && value.accountIds.length === 0)
  await area('psql').getByRole('button', { name: '删除集合配置，恢复旧模式', exact: true }).click()
  await page.getByLabel('psql 项目账号', { exact: true }).waitFor()
  assert.equal((await getDB('psql')).mode, 'legacy_single')
  await checkbox('psql', a.name).check(); await checkbox('psql', b.name).check()
  await area('psql').getByRole('button', { name: '保存连接集合', exact: true }).click()
  await until(() => getDB('psql'), value => value.accountIds.length === 2)
  await area('mysql').getByRole('button', { name: '保存连接集合', exact: true }).click()
  await until(() => getDB('mysql'), value => value.mode === 'explicit_set')
  ok('actual empty PUT and DELETE restoration differ; psql/mysql collections stay independent')

  await page.goto(web.origin + base + '/settings')
  const row = page.getByRole('region', { name: 'CLI 账号', exact: true }).getByRole('listitem').filter({ has: page.getByRole('heading', { name: a.name, exact: true }) })
  await row.getByRole('button', { name: '删除账号', exact: true }).click()
  const dialog = page.getByRole('alertdialog')
  await dialog.getByText(/WebDBConnectionWorker（连接集合）/).waitFor()
  await dialog.getByRole('button', { name: '删除账号', exact: true }).click()
  await dialog.waitFor({ state: 'hidden' })
  await page.goto(web.origin + config)
  await area('psql').getByText('账号已删除', { exact: true }).waitFor()
  const deleted = await getDB('psql')
  assert.equal(deleted.status, 'account_deleted'); assert.deepEqual(deleted.accountIds.sort(), [a.id, b.id].sort())
  assert.equal((await getDB('mysql')).status, 'resolved')
  for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
    await page.setViewportSize(viewport)
    await area('psql').screenshot({ path: new URL(`db-${viewport.width}.png`, out).pathname })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
    screenshots.push({ ...viewport, boundary: 'psql region screenshot may exceed viewport height' })
  }
  ok('actual account delete impact labels collection; original member ID retained and invalid, other CLI unchanged; desktop/390 no overflow')
  await page.goto(web.origin + `/platform/agents/${manifest.platformAgentId}/config`)
  await page.getByRole('button', { name: '录入凭证', exact: true }).waitFor()
  assert.equal(await page.getByText('psql 授权连接集合', { exact: true }).count(), 0)
  ok('actual platform psql remains private credential UI, no project collection editor')
  assert.deepEqual(errors, []); assert.deepEqual(denied, [])
  assert.ok(forwarded.some(item => item.status === 412 && item.path.endsWith('/db-connections/psql')))
  writeFileSync(new URL('browser-results.json', out), JSON.stringify({ boundary: 'actual React -> forwarded temporary App -> isolated PG; no model/OAuth/native database execution', checks, ids, forwarded, errors, denied, screenshots }, null, 2))
} catch (error) {
  writeFileSync(new URL('browser-failure.json', out), JSON.stringify({ errorType: error.name, message: error.message, checks, ids, forwarded, errors, denied }, null, 2))
  process.exitCode = 1
} finally {
  await browser?.close()
  if (preview && preview.exitCode === null) { preview.kill('SIGTERM'); await new Promise(resolve => { const timeout = setTimeout(resolve, 3000); preview.once('exit', () => { clearTimeout(timeout); resolve() }) }) }
}
