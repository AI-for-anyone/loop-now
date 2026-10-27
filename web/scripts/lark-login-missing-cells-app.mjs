/* global document, window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'

const manifest = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_LARK_MANIFEST, 'utf8'))
const api = new URL(manifest.apiBase), web = new URL(manifest.webOrigin)
assert.equal(api.protocol, 'http:'); assert.equal(api.hostname, '127.0.0.1'); assert.equal(api.pathname, '/api/v1')
assert.ok(!['8780', '8781', '5173', ''].includes(api.port)); assert.equal(web.origin, 'http://127.0.0.1:4334')
assert.ok(!api.username && !api.password && !api.search && !api.hash)
mkdirSync(manifest.evidenceDir, { recursive: true })
const checks = [], denied = [], errors = []
let browser, preview, forwarded = [], failedId = null
const root = fileURLToPath(new URL('../', import.meta.url))
const save = (file, value) => writeFileSync(manifest.evidenceDir + '/' + file, JSON.stringify(value, null, 2), { mode: 0o600 })
try {
  await new Promise((resolve, reject) => {
    preview = spawn(process.execPath, [root + 'node_modules/vite/bin/vite.js', '--host', '127.0.0.1', '--port', web.port, '--strictPort'], { cwd: root, env: { PATH: process.env.PATH }, stdio: ['ignore', 'pipe', 'pipe'] })
    const timer = setTimeout(() => reject(new Error('preview readiness')), 15000)
    preview.stdout.on('data', b => { if (b.toString().includes(web.origin)) { clearTimeout(timer); resolve() } })
    preview.once('error', () => { clearTimeout(timer); reject(new Error('preview spawn')) })
    preview.once('exit', () => { clearTimeout(timer); reject(new Error('preview exit')) })
  })
  browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  await context.route('**/*', route => {
    const url = new URL(route.request().url())
    if (url.origin === web.origin || url.origin === api.origin) return route.fallback()
    denied.push({ method: route.request().method(), origin: url.origin, path: url.pathname }); return route.abort()
  })
  const page = await context.newPage(); page.on('pageerror', () => errors.push('pageerror'))
  forwarded = await forwardTemporaryAPI(page, manifest.apiBase, web.origin)
  const get = async path => {
    const response = await context.request.get(manifest.apiBase + path, { headers: { origin: web.origin }, maxRedirects: 0, maxRetries: 0 })
    assert.equal(response.status(), 200); return response.json()
  }
  const accountBase = `/projects/${manifest.projectId}/credential-accounts`
  const before = (await get(accountBase)).accounts
  const source = before.find(a => a.id === manifest.sourceAccountId), user = before.find(a => a.id === manifest.userAccountId)
  assert.ok(source && user && user.larkIdentity)
  const region = () => page.getByRole('region', { name: 'CLI 账号', exact: true })
  const row = name => region().getByRole('listitem').filter({ has: page.getByRole('heading', { name, exact: true }) })
  await page.goto(web.origin + `/p/${encodeURIComponent(manifest.projectName)}/settings`)
  await region().getByLabel('添加 CLI 账号 · 工具').selectOption('lark-cli')
  // There is already one real saved user. Its public re-login stays disabled.
  assert.equal(await row(user.name).getByRole('button', { name: '重新登录此用户账号', exact: true }).isDisabled(), true)
  await region().getByLabel('授权使用的项目应用', { exact: true }).selectOption(source.id)
  const accepted = page.waitForResponse(r => r.request().method() === 'POST' && new URL(r.url()).pathname.endsWith('/cli-auth/operations'))
  await region().getByRole('button', { name: '登录飞书用户', exact: true }).click()
  const response = await accepted; assert.equal(response.status(), 202)
  failedId = response.request().postDataJSON().operationId
  assert.equal(new URL(page.url()).searchParams.get(`cliAuth.${manifest.projectId}.lark-cli.new`), failedId)
  await region().getByText('本次授权失败，原账号版本保留', { exact: true }).waitFor({ timeout: 10000 })
  const afterFailure = (await get(accountBase)).accounts
  for (const old of [source, user]) {
    const current = afterFailure.find(a => a.id === old.id)
    assert.equal(current.currentVersionId, old.currentVersionId); assert.equal(current.revision, old.revision)
    assert.deepEqual(current.larkIdentity, old.larkIdentity)
  }
  assert.equal(afterFailure.length, before.length)
  const failed = await get(`/projects/${manifest.projectId}/cli-auth/operations/${failedId}`)
  assert.equal(failed.operation.status, 'failed'); assert.equal(failed.operation.authorizationUrl, null); assert.equal(failed.operation.userCode, null)
  checks.push('one actual new-login failure retains existing bot and user identity/version/revision; public re-login remains disabled, not exercised')
  const remove = async account => {
    const impact = page.waitForResponse(r => { const u = new URL(r.url()); return u.pathname.endsWith('/credential-account-impact') && u.searchParams.get('accountId') === account.id })
    await row(account.name).getByRole('button', { name: '删除账号', exact: true }).click()
    const dialog = page.getByRole('alertdialog', { name: `删除账号「${account.name}」？`, exact: true })
    await dialog.getByText('当前配置使用者影响', { exact: true }).waitFor()
    assert.equal((await impact).status(), 200)
    const removed = page.waitForResponse(r => r.request().method() === 'DELETE' && new URL(r.url()).pathname.endsWith('/credential-accounts/' + account.id))
    await dialog.getByRole('button', { name: '删除账号', exact: true }).click()
    assert.equal((await removed).status(), 200)
    await row(account.name).waitFor({ state: 'hidden' })
  }
  await remove(source)
  const retained = await get(accountBase + '/' + user.id)
  assert.equal(retained.currentVersionId, user.currentVersionId); assert.equal(retained.status, 'logged_in'); assert.deepEqual(retained.larkIdentity, user.larkIdentity)
  await row(user.name).getByText(new RegExp(user.larkIdentity.openId)).first().waitFor()
  checks.push('actual React impact/DELETE source bot leaves saved user independent with same verified identity and fixed version')
  await remove(user)
  assert.equal((await get(accountBase)).accounts.length, 0)
  checks.push('actual React impact/DELETE user removes it from new selection; retained PG history audited by harness, no claim about old running tasks')
  const posts = forwarded.filter(r => r.method === 'POST')
  assert.equal(posts.length, 1); assert.equal(posts[0].status, 202)
  const deletes = forwarded.filter(r => r.method === 'DELETE')
  assert.equal(deletes.length, 2); assert.ok(deletes.every(r => r.status === 200))
  assert.deepEqual(errors, []); assert.deepEqual(denied, [])
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
  await page.screenshot({ path: manifest.evidenceDir + '/failure-delete-1440.png', fullPage: true })
  save('browser-results.json', { passed: true, checks, failedId, forwarded, errors, denied, level: 'actual desktop React/temporary App; loopback synthetic failure; no real OAuth/native/task/re-login execution' })
  await context.close()
} catch (error) {
  save('browser-failure.json', { passed: false, errorType: error.name, sourceLine: error.stack?.match(/lark-login-missing-cells-app\.mjs:\d+:\d+/)?.[0] ?? null, checks, failedId, forwarded, errors, denied }); process.exitCode = 1
} finally {
  await browser?.close()
  if (preview && preview.exitCode === null) { preview.kill('SIGTERM'); await new Promise(resolve => { const timer = setTimeout(resolve, 3000); preview.once('exit', () => { clearTimeout(timer); resolve() }) }) }
}
