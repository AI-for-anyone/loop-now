/* global document, window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'

// The Go owner supplies only temporary loopback addresses and synthetic IDs.
// No browser HTTP response is invented except the intercepted issuer page.
const manifest = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_LARK_MANIFEST, 'utf8'))
const api = new URL(manifest.apiBase), web = new URL(manifest.webOrigin)
assert.equal(api.hostname, '127.0.0.1'); assert.equal(api.protocol, 'http:')
assert.equal(api.pathname, '/api/v1'); assert.ok(!['8780', '8781', '5173', ''].includes(api.port))
assert.equal(web.origin, 'http://127.0.0.1:4334')
assert.ok(!api.username && !api.password && !api.search && !api.hash)
const out = manifest.evidenceDir
mkdirSync(out, { recursive: true })
const checks = [], denied = [], errors = [], issuer = [], starts = [], faults = []
let browser, preview, forwarded = []
const root = fileURLToPath(new URL('../', import.meta.url))
const write = (name, value) => writeFileSync(out + '/' + name, JSON.stringify(value, null, 2), { mode: 0o600 })
const startPreview = () => new Promise((resolve, reject) => {
  preview = spawn(process.execPath, [root + 'node_modules/vite/bin/vite.js', '--host', '127.0.0.1', '--port', web.port, '--strictPort'], { cwd: root, env: { PATH: process.env.PATH }, stdio: ['ignore', 'pipe', 'pipe'] })
  const timer = setTimeout(() => reject(new Error('frontend readiness')), 15000)
  preview.stdout.on('data', b => { if (b.toString().includes(web.origin)) { clearTimeout(timer); resolve() } })
  preview.once('error', () => { clearTimeout(timer); reject(new Error('frontend spawn')) })
  preview.once('exit', () => { clearTimeout(timer); reject(new Error('frontend exit')) })
})
try {
  await startPreview(); browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  // Install before any page or popup. Never permit a real issuer or shared API.
  await context.route('**/*', route => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin === 'https://accounts.feishu.cn' && url.pathname === '/oauth/v1/device/verify') {
      issuer.push({ origin: url.origin, path: url.pathname })
      return route.fulfill({ contentType: 'text/html', body: '<title>Offline Lark issuer</title><p>Injected navigation boundary</p>' })
    }
    if (url.origin === web.origin) return route.fallback()
    // Page routing below forwards these requests, never continues to 8780.
    if (url.origin === 'http://127.0.0.1:8780' && url.pathname.startsWith('/api/v1/')) {
      denied.push({ method: request.method(), path: url.pathname, reason: 'unhandled API' }); return route.abort()
    }
    if (url.origin === api.origin) return route.fallback()
    denied.push({ method: request.method(), origin: url.origin, path: url.pathname }); return route.abort()
  })
  const page = await context.newPage()
  page.on('pageerror', () => errors.push('pageerror'))
  forwarded = await forwardTemporaryAPI(page, manifest.apiBase, web.origin)
  const get = async path => {
    const response = await context.request.get(manifest.apiBase + path, { headers: { origin: web.origin }, maxRedirects: 0, maxRetries: 0 })
    assert.equal(response.status(), 200); return response.json()
  }
  const status = id => get(`/projects/${manifest.projectId}/cli-auth/operations/${id}`)
  const waitStatus = async (id, expected) => {
    for (let i = 0; i < 100; i++) { const result = await status(id); if (result.operation.status === expected) return result; await page.waitForTimeout(100) }
    throw new Error('safe operation status deadline')
  }
  const control = (id, action) => writeFileSync(manifest.controlDir + '/' + id + '.json', JSON.stringify({ action }), { mode: 0o600 })
  const region = () => page.getByRole('region', { name: 'CLI 账号', exact: true })
  const login = () => region().getByRole('region', { name: '飞书用户登录', exact: true })
  const accounts = () => get(`/projects/${manifest.projectId}/credential-accounts`)
  const before = await accounts()
  let source = before.accounts.find(a => a.id === manifest.sourceAccountId)
  assert.ok(source); assert.notEqual(source.authMethod, 'oauth'); assert.ok(source.larkApplication)
  await page.goto(web.origin + `/p/${encodeURIComponent(manifest.projectName)}/settings`)
  await region().getByLabel('添加 CLI 账号 · 工具').selectOption('lark-cli')
  const start = async () => {
    await login().getByLabel('授权使用的项目应用', { exact: true }).selectOption(source.id)
    const received = page.waitForRequest(req => req.method() === 'POST' && new URL(req.url()).pathname.endsWith('/cli-auth/operations'))
    const accepted = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith('/cli-auth/operations'))
    await login().getByRole('button', { name: /^(登录飞书用户|重新发起登录)$/ }).click()
    const req = await received, body = req.postDataJSON()
    assert.deepEqual(Object.keys(body).sort(), ['authType', 'cli', 'expectedSourceRevision', 'operationId', 'sourceAccountId'])
    assert.equal(body.sourceAccountId, source.id); assert.equal(body.expectedSourceRevision, source.revision)
    assert.equal(new URL(page.url()).searchParams.get(`cliAuth.${manifest.projectId}.lark-cli.new`), body.operationId)
    starts.push(body.operationId)
    assert.equal((await accepted).status(), 202)
    await waitStatus(body.operationId, 'awaiting_authorization')
    await login().getByRole('link', { name: '打开官方授权页', exact: true }).waitFor()
    return body.operationId
  }
  const first = await start()
  assert.equal(context.pages().length, 1)
  await page.reload()
  await login().getByRole('link', { name: '打开官方授权页', exact: true }).waitFor()
  assert.equal(starts.length, 1)
  const popupReady = page.waitForEvent('popup')
  await login().getByRole('link', { name: '打开官方授权页', exact: true }).click()
  const popup = await popupReady
  await popup.waitForURL(url => url.origin === 'https://accounts.feishu.cn' && url.pathname === '/oauth/v1/device/verify')
  assert.equal(await popup.evaluate(() => window.opener === null), true); await popup.close()
  checks.push('one strict source POST, route original ID, real GET reload, native isolated issuer without preopened blank')
  control(1, 'succeed'); const saved = await waitStatus(first, 'succeeded')
  assert.equal(saved.operation.authorizationUrl, null); assert.equal(saved.operation.userCode, null)
  assert.equal(saved.account.authMethod, 'oauth'); assert.ok(saved.account.larkIdentity)
  assert.notEqual(saved.account.id, source.id)
  await page.reload()
  const after = await accounts(), unchanged = after.accounts.find(a => a.id === source.id)
  assert.equal(unchanged.currentVersionId, source.currentVersionId); assert.equal(unchanged.revision, source.revision)
  assert.equal(after.accounts.length, before.accounts.length + 1)
  await region().getByText(saved.account.larkIdentity.openId, { exact: false }).first().waitFor()
  checks.push('actual App saves separate verified user/version while original bot/version remains unchanged')
  // Explicit new user action; no automatic retry of the succeeded operation.
  const second = await start()
  await login().getByRole('button', { name: '取消本次授权', exact: true }).click()
  await waitStatus(second, 'cancelled'); control(2, 'succeed')
  await page.reload(); await login().getByText('本次授权已取消，原账号版本保留', { exact: true }).waitFor()
  assert.equal((await accounts()).accounts.length, after.accounts.length)
  assert.equal(await login().getByRole('link', { name: '打开官方授权页' }).count(), 0)
  checks.push('real cancel terminal, no late helper account, original success/account retained')
  // Change the source in the real TEST App after the form captured its old
  // revision. Do not teach the UI to silently select the updated application.
  await login().getByLabel('授权使用的项目应用', { exact: true }).selectOption(source.id)
  const renamed = await context.request.patch(manifest.apiBase + `/projects/${manifest.projectId}/credential-accounts/${source.id}`, {
    headers: { origin: web.origin }, data: { name: '桌面原应用修订', expectedRevision: source.revision }, maxRedirects: 0, maxRetries: 0,
  })
  assert.equal(renamed.status(), 200)
  const rejected = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/cli-auth/operations'))
  await login().getByRole('button', { name: '重新发起登录', exact: true }).click()
  assert.equal((await rejected).status(), 412)
  await page.waitForFunction(key => !new URLSearchParams(window.location.search).has(key), `cliAuth.${manifest.projectId}.lark-cli.new`)
  assert.equal((await accounts()).accounts.length, after.accounts.length)
  checks.push('actual TEST source revision changes after selection: one stale POST rejected412, no new account or authorization')
  await page.reload()
  await region().getByLabel('添加 CLI 账号 · 工具').selectOption('lark-cli')
  source = (await accounts()).accounts.find(a => a.id === source.id)
  await login().getByLabel('授权使用的项目应用', { exact: true }).selectOption(source.id)
  // Drop only the browser's response AFTER the actual temporary App accepted
  // this single POST. Reload must GET that original committed operation.
  let droppedId = null
  const drop = async route => {
    const request = route.request()
    if (request.method() !== 'POST') return route.fallback()
    const body = request.postDataJSON(); droppedId = body.operationId
    assert.equal(new URL(page.url()).searchParams.get(`cliAuth.${manifest.projectId}.lark-cli.new`), droppedId)
    const response = await route.fetch({ url: manifest.apiBase + `/projects/${manifest.projectId}/cli-auth/operations`, headers: { ...request.headers(), origin: web.origin, host: api.host }, maxRedirects: 0, maxRetries: 0 })
    faults.push({ id: droppedId, actualStatus: response.status(), phase: 'after actual App acceptance, browser response aborted' })
    assert.equal(response.status(), 202)
    await route.abort('failed')
  }
  const pattern = 'http://127.0.0.1:8780/api/v1/projects/*/cli-auth/operations'
  await page.route(pattern, drop)
  await login().getByRole('button', { name: '登录飞书用户', exact: true }).click()
  await page.waitForFunction(key => !!new URLSearchParams(window.location.search).get(key), `cliAuth.${manifest.projectId}.lark-cli.new`)
  for (let i = 0; i < 100 && !faults.length; i++) await page.waitForTimeout(50)
  assert.equal(faults.length, 1); starts.push(droppedId)
  await waitStatus(droppedId, 'awaiting_authorization')
  await page.reload()
  await login().getByRole('link', { name: '打开官方授权页', exact: true }).waitFor()
  assert.equal(new URL(page.url()).searchParams.get(`cliAuth.${manifest.projectId}.lark-cli.new`), droppedId)
  assert.equal(faults.length, 1)
  control(3, 'succeed'); const recovered = await waitStatus(droppedId, 'succeeded')
  assert.equal(recovered.operation.authorizationUrl, null); assert.equal(recovered.operation.userCode, null)
  assert.equal((await accounts()).accounts.length, after.accounts.length + 1)
  await page.unroute(pattern, drop)
  await page.reload()
  checks.push('actual App accepted start response dropped: original route ID reloadGET recovers same operation; exactly one POST, independent verified user saved')
  for (const viewport of [{ width: 1440, height: 1000 }]) {
    await page.setViewportSize(viewport)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
    await page.screenshot({ path: out + `/saved-${viewport.width}.png`, fullPage: true })
  }
  assert.deepEqual(denied, []); assert.deepEqual(errors, [])
  const writes = forwarded.filter(r => r.method === 'POST' && r.path.endsWith('/cli-auth/operations'))
  assert.equal(writes.length, 3); assert.deepEqual(writes.map(r => r.status).sort(), [202, 202, 412])
  assert.equal(issuer.length, 1)
  assert.ok(!forwarded.some(r => r.method !== 'GET' && /messages|tasks|model-auth|credential-selections/.test(r.path)))
  write('browser-results.json', { passed: true, boundary: 'actual React/forwarded temporary App; TEST PG; injected helper and locally fulfilled issuer, not OAuth/runtime/QA', checks, starts, faults, issuer, forwarded, denied, errors })
  await context.close()
} catch (error) {
  const sourceLine = error.stack?.match(/lark-login-app\.mjs:\d+:\d+/)?.[0] ?? null
  write('browser-failure.json', { passed: false, errorType: error.name, sourceLine, checks, starts, faults, issuer, forwarded, denied, errors })
  process.exitCode = 1
} finally {
  await browser?.close()
  if (preview && preview.exitCode === null) {
    preview.kill('SIGTERM')
    await new Promise(resolve => { const timer = setTimeout(resolve, 3000); preview.once('exit', () => { clearTimeout(timer); resolve() }) })
  }
}
