/* global document, window */
// Desktop-only actual App driver. No response/manager/participant success stub.
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'

const input = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_LARK_SWITCH_MANIFEST, 'utf8'))
const api = new URL(input.apiBase), web = new URL(input.webOrigin)
assert.equal(api.protocol, 'http:'); assert.equal(api.hostname, '127.0.0.1')
assert.equal(api.pathname, '/api/v1'); assert.ok(!['8780', '8781', '5173', ''].includes(api.port))
assert.equal(web.origin, 'http://127.0.0.1:4336')
assert.ok(['commit', 'unknown', 'target-unavailable', 'missing-participants', 'native-commit'].includes(input.slice))
assert.ok(!api.search && !api.hash && !api.username && !api.password)
mkdirSync(input.evidenceDir, { recursive: true })
const checks = [], denied = [], errors = [], starts = [], faults = []
let browser, preview, forwarded = []
const root = fileURLToPath(new URL('../', import.meta.url))
const save = (file, value) => writeFileSync(input.evidenceDir + '/' + file, JSON.stringify(value, null, 2), { mode: 0o600 })
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
    const request = route.request(), url = new URL(request.url())
    if (url.origin === web.origin || url.origin === api.origin) return route.fallback()
    // page forwarding runs before this context guard; any unhandled shared
    // backend/provider navigation is denied, including every popup.
    denied.push({ method: request.method(), origin: url.origin, path: url.pathname })
    return route.abort()
  })
  const page = await context.newPage()
  page.on('pageerror', () => errors.push('pageerror'))
  forwarded = await forwardTemporaryAPI(page, input.apiBase, web.origin)
  const actionPath = `/tasks/${input.taskId}/lark-account-switches`
  const routeKey = `taskLarkSwitch.${input.taskId}`
  const pattern = 'http://127.0.0.1:8780/api/v1' + actionPath
  if (input.slice === 'target-unavailable') await page.route(pattern, async route => {
    if (route.request().method() !== 'POST') return route.fallback()
    writeFileSync(input.controlDir + '/invalidate-target', '', { mode: 0o600 })
    for (let i = 0; i < 100 && !existsSync(input.controlDir + '/target-invalidated'); i++) await page.waitForTimeout(30)
    assert.equal(existsSync(input.controlDir + '/target-invalidated'), true)
    return route.fallback()
  })
  if (input.slice === 'unknown') await page.route(pattern, async route => {
    const request = route.request()
    if (request.method() !== 'POST') return route.fallback()
    const body = request.postDataJSON()
    const response = await route.fetch({ url: input.apiBase + actionPath, headers: { ...request.headers(), origin: web.origin, host: api.host }, maxRedirects: 0, maxRetries: 0 })
    assert.equal(response.status(), 202)
    faults.push({ actionId: body.actionId, status: response.status(), phase: 'actual App response lost after acceptance' })
    await route.abort('failed')
  })
  await page.goto(web.origin + `/p/${encodeURIComponent(input.projectName)}/tasks/${input.taskId}`)
  const action = page.getByRole('button', { name: '更换 Lark 账号并继续 QA', exact: true })
  await action.waitFor()
  if (input.slice === 'missing-participants') {
    assert.equal(await action.isDisabled(), true)
    await page.getByText('当前服务尚未提供本任务可核对的 Lark 参与者，切换能力未就绪。', { exact: true }).waitFor()
    checks.push('real TaskDetail without participants keeps confirmation unavailable; no inferred member or POST')
  } else {
    assert.equal(await page.getByLabel('明确确认当前提测成员', { exact: true }).inputValue(), '')
    assert.equal(await page.getByLabel('明确选择结果跟进成员', { exact: true }).inputValue(), '')
    await page.getByLabel('明确确认当前提测成员', { exact: true }).selectOption(input.expectedRuntimeId)
    const followup = input.expectedMembers.find(m => m.runtimeId !== input.expectedRuntimeId)
    assert.ok(followup)
    await page.getByLabel('明确选择结果跟进成员', { exact: true }).selectOption(followup.runtimeId)
    const sent = page.waitForRequest(request => request.method() === 'POST' && new URL(request.url()).pathname.endsWith(actionPath))
    const accepted = input.slice !== 'unknown' ? page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname.endsWith(actionPath)) : null
    await action.click()
    const dialog = page.getByRole('alertdialog')
    await dialog.waitFor()
    for (const member of input.expectedMembers) {
      assert.equal(await dialog.getByText(new RegExp(member.agentId + ' · ' + member.runtimeId)).count(), 1)
    }
    await dialog.getByLabel('本次固定用户账号版本', { exact: true }).selectOption(input.targetAccountId)
    await dialog.getByRole('button', { name: '确认更换并继续 QA', exact: true }).click()
    const request = await sent, body = request.postDataJSON(); starts.push(body.actionId)
    assert.equal(new URL(page.url()).searchParams.get(routeKey), body.actionId)
    assert.equal(body.expectedTaskVersion, input.expectedTaskVersion)
    assert.equal(body.expectedNodeRunId, input.expectedNodeRunId)
    assert.equal(body.targetAccountId, input.targetAccountId); assert.equal(body.targetVersionId, input.targetVersionId)
    assert.deepEqual([...body.expectedMembers].sort((a, b) => a.runtimeId.localeCompare(b.runtimeId)), [...input.expectedMembers].sort((a, b) => a.runtimeId.localeCompare(b.runtimeId)))
    if (accepted) assert.equal((await accepted).status(), input.slice === 'target-unavailable' ? 409 : 202)
    else { for (let i = 0; i < 100 && !faults.length; i++) await page.waitForTimeout(50); assert.equal(faults.length, 1) }
    if (input.slice === 'target-unavailable') {
      await page.getByText('尚未找到原操作，不能确认是否已提交。不会更换操作 ID 或自动重发。', { exact: true }).waitFor()
      assert.equal(await dialog.getByLabel('本次固定用户账号版本', { exact: true }).inputValue(), input.targetAccountId)
      assert.equal(await dialog.getByRole('button', { name: '确认更换并继续 QA', exact: true }).isDisabled(), true)
    }
    await page.reload()
    await page.getByText(input.slice === 'target-unavailable' ? '尚未找到原操作，不能确认是否已提交。不会更换操作 ID 或自动重发。' : '固定绑定与唯一 QA continuation 已提交；不代表 QA 已发送或通过。', { exact: true }).waitFor({ timeout: 20000 })
    assert.equal(new URL(page.url()).searchParams.get(routeKey), body.actionId)
    checks.push(input.slice === 'target-unavailable' ? 'actual target grant becomes unavailable immediately before POST:409 and no publication; original target selection held, reload onlyGET original404 and no repost' : 'exact real participant group and target version, one POST after route commitment, reload recovers original receipt; no QA outcome claim')
    if (input.slice === 'unknown') { assert.equal(faults.length, 1); assert.equal(faults[0].actionId, body.actionId) }
  }
  assert.deepEqual(denied, []); assert.deepEqual(errors, [])
  const posts = forwarded.filter(r => r.method === 'POST')
  assert.ok(posts.every(r => r.path.endsWith(actionPath)))
  assert.equal(posts.length + faults.length, input.slice === 'missing-participants' ? 0 : 1)
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
  await page.screenshot({ path: input.evidenceDir + '/task-1440.png', fullPage: true })
  save('browser-results.json', { passed: true, checks, starts, faults, forwarded, denied, errors, boundary: 'actual React -> temporary App; no native-manager success stub or provider navigation; PG/native assertions belong to Go harness' })
  await context.close()
} catch (error) {
  save('browser-failure.json', { passed: false, errorType: error.name, sourceLine: error.stack?.match(/lark-switch-app\.mjs:\d+:\d+/)?.[0] ?? null, checks, starts, faults, forwarded, denied, errors })
  process.exitCode = 1
} finally {
  await browser?.close()
  if (preview && preview.exitCode === null) {
    preview.kill('SIGTERM')
    await new Promise(resolve => { const timer = setTimeout(resolve, 3000); preview.once('exit', () => { clearTimeout(timer); resolve() }) })
  }
}
