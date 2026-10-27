/* global document, window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'

const manifest = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_DB_MANIFEST, 'utf8'))
const api = new URL(manifest.apiBase), web = new URL(manifest.webOrigin)
assert.equal(manifest.slice, 'sa'); assert.equal(api.hostname, '127.0.0.1'); assert.equal(api.protocol, 'http:')
assert.equal(api.pathname, '/api/v1'); assert.ok(!['8780', '8781', '5173', ''].includes(api.port))
assert.equal(web.origin, 'http://127.0.0.1:4318'); assert.ok(!api.username && !api.password && !api.search && !api.hash)
const out = new URL('../test-results/add-cloud-database-cli-templates/app-sa/', import.meta.url)
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
try {
  await startPreview(); browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  const page = await context.newPage()
  page.on('pageerror', () => errors.push('pageerror'))
  forwarded = await forwardTemporaryAPI(page, manifest.apiBase, web.origin)
  await page.route('**/*', route => {
    const request = route.request(), url = new URL(request.url())
    const writeAllowed = /\/(credential-accounts(?:\/[^/]+(?:\/credential)?)?|credential-selections)$/.test(url.pathname)
    if (![web.origin, api.origin, 'http://127.0.0.1:8780'].includes(url.origin) || (request.method() !== 'GET' && !writeAllowed)) {
      denied.push({ method: request.method(), path: url.pathname }); return route.abort()
    }
    return route.fallback()
  })
  const get = async path => {
    const response = await context.request.get(manifest.apiBase + path, { headers: { origin: web.origin }, maxRedirects: 0, maxRetries: 0 })
    assert.equal(response.status(), 200); return response.json()
  }
  const base = `/p/${encodeURIComponent(manifest.projectName)}`, config = `${base}/agents/${manifest.agentId}/config`
  const region = () => page.getByRole('region', { name: 'CLI 账号', exact: true })
  const row = name => region().getByRole('listitem').filter({ has: page.getByRole('heading', { name, exact: true }) })
  const selections = async () => (await get(`/agents/${manifest.agentId}/credential-selections`)).selections.find(item => item.purpose === 'cli' && item.cli === 'gcloud')
  const create = async (name, json, capture = false) => {
    await page.goto(web.origin + base + '/settings')
    await region().getByLabel('添加 CLI 账号 · 工具').selectOption('gcloud')
    await region().getByRole('button', { name: '录入服务账号 JSON', exact: true }).click()
    await region().getByLabel('账号名称', { exact: true }).fill(name)
    const input = region().getByLabel('服务账号 JSON', { exact: false })
    assert.equal(await input.evaluate(element => element.tagName), 'TEXTAREA')
    await input.fill(json)
    assert.equal(await input.evaluate(element => window.getComputedStyle(element).color), 'rgba(0, 0, 0, 0)')
    await region().getByLabel('默认 GCP 项目（可选）', { exact: true }).fill('synthetic-target')
    if (capture) for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
      await page.setViewportSize(viewport); await region().screenshot({ path: new URL(`sa-${viewport.width}.png`, out).pathname })
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
      screenshots.push({ ...viewport, boundary: 'CLI region may exceed viewport height; all private characters masked' })
    }
    await region().getByRole('button', { name: '保存', exact: true }).click()
    await region().getByRole('heading', { name, exact: true }).waitFor()
    await input.waitFor({ state: 'hidden' })
    const account = (await get(`/projects/${manifest.projectId}/credential-accounts`)).accounts.find(item => item.name === name)
    assert.ok(account); assert.equal(account.authMethod, 'service_account')
    assert.ok(!JSON.stringify(account).includes('PRIVATE KEY')); return account
  }
  const a = await create('合成Google SA账号A', manifest.sa1, true); ids.a = a.id
  await page.goto(web.origin + config)
  await page.getByText('当前自动取用账号：', { exact: false }).filter({ hasText: a.name }).waitFor()
  assert.equal((await selections()).resolvedAccountId, a.id)
  checks.push('actual React SA JSON saved with service_account metadata, multiline masked and cleared; one account automatic legacy selection')
  const b = await create('合成Google SA账号B', manifest.sa1); ids.b = b.id
  await page.goto(web.origin + config)
  const select = page.getByLabel('gcloud 项目账号', { exact: true })
  await select.selectOption(b.id)
  await select.locator('..').getByRole('button', { name: '保存选择', exact: true }).click()
  await page.getByText('明确选择的账号：', { exact: false }).filter({ hasText: b.name }).waitFor()
  assert.equal((await selections()).resolvedAccountId, b.id)
  await page.goto(web.origin + base + '/settings')
  await row(b.name).getByRole('button', { name: '整体替换凭证', exact: true }).click()
  await row(b.name).getByLabel('服务账号 JSON', { exact: false }).fill(manifest.sa2)
  await row(b.name).getByLabel('默认 GCP 项目（可选）', { exact: true }).fill('synthetic-replacement')
  await row(b.name).getByRole('button', { name: '保存', exact: true }).click()
  await row(b.name).getByLabel('服务账号 JSON', { exact: false }).waitFor({ state: 'hidden' })
  const latestB = await get(`/projects/${manifest.projectId}/credential-accounts/${b.id}`)
  assert.notEqual(latestB.currentVersionId, b.currentVersionId); assert.equal(latestB.authMethod, 'service_account')
  assert.equal((await get(`/projects/${manifest.projectId}/credential-accounts/${a.id}`)).currentVersionId, a.currentVersionId)
  checks.push('actual two SA accounts require explicit gcloud single selection; replace B changes only B version and leaves A')
  await row(b.name).getByRole('button', { name: '删除账号', exact: true }).click()
  const dialog = page.getByRole('alertdialog')
  await dialog.getByText(/WebDBConnectionWorker（明确选择）/).waitFor()
  await dialog.getByRole('button', { name: '删除账号', exact: true }).click(); await dialog.waitFor({ state: 'hidden' })
  await page.goto(web.origin + config)
  await page.getByText('明确选择的账号已删除，不自动换用其他账号', { exact: true }).waitFor()
  assert.equal((await selections()).selectedAccountId, b.id)
  checks.push('actual SA delete impact and invalid explicit ID retained without switching back to A')
  await page.goto(web.origin + `/platform/agents/${manifest.platformAgentId}/config`)
  assert.equal(await page.getByText('gcloud 授权连接集合', { exact: true }).count(), 0)
  checks.push('platform remains private credential UI; no project collection or Google management action dispatched')
  assert.deepEqual(errors, []); assert.deepEqual(denied, [])
  assert.ok(!forwarded.some(item => item.method !== 'GET' && /cli-auth|model-auth|messages/.test(item.path)))
  writeFileSync(new URL('browser-results.json', out), JSON.stringify({ boundary: 'actual React -> forwarded isolated App/PG static SA CRUD/selection; zero Google/OAuth/native execution', checks, ids, forwarded, errors, denied, screenshots }, null, 2))
} catch (error) {
  // No request bodies, private JSON or provider content in the audit.
  writeFileSync(new URL('browser-failure.json', out), JSON.stringify({ errorType: error.name, message: error.message, checks, ids, forwarded, errors, denied }, null, 2)); process.exitCode = 1
} finally {
  await browser?.close()
  if (preview && preview.exitCode === null) { preview.kill('SIGTERM'); await new Promise(resolve => { const timeout = setTimeout(resolve, 3000); preview.once('exit', () => { clearTimeout(timeout); resolve() }) }) }
}
