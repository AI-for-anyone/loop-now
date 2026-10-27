/* global window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync } from 'node:fs'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'
const f = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_SLEEP_MANIFEST, 'utf8'))
assert.ok(['ordinary', 'race'].includes(f.phase))
const dir = new URL(`../test-results/add-agent-sleep/${f.phase}/`, import.meta.url)
const checks = [], errors = [], blocked = []
let phase = 'initialize'
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
await context.addInitScript(() => {
  window.__sleepEvents = []
  const Actual = window.EventSource
  window.EventSource = class extends Actual {
    constructor(...args) {
      super(...args)
      this.addEventListener('sleep.updated', event => {
        const data = JSON.parse(event.data)
        window.__sleepEvents.push({ scopeKind: data.scopeKind, taskId: data.taskId, taskVersion: data.taskVersion, sleepRevision: data.sleepRevision, sleepId: data.sleep?.id ?? null, endedSleepId: data.endedSleepId })
      })
    }
  }
})
await context.route('**/*', route => {
  const url = new URL(route.request().url())
  if (url.origin === 'http://localhost:5173') return route.continue()
  blocked.push({ method: route.request().method(), host: url.hostname, path: url.pathname })
  return route.abort()
})
const page = await context.newPage(), taskPage = await context.newPage()
const forwarded = []
for (const p of [page, taskPage]) {
  p.on('pageerror', error => errors.push({ name: error.name, message: error.message, stack: error.stack }))
  forwarded.push(await forwardTemporaryAPI(p, f.apiBase))
}
const origin = 'http://localhost:5173', base = `${origin}/p/${encodeURIComponent(f.projectName)}`
const chat = `${base}/agents/${f.agentId}/chat`, task = id => `${base}/tasks/${id}`
const status = async () => (await fetch(`${f.apiBase}/agents/${f.agentId}/status`)).json()
const facts = async id => (await fetch(`${f.apiBase}/tasks/${id}`)).json()
const control = async action => { const r = await fetch(`${f.controlBase}/${action}`, { method: 'POST' }); assert.ok(r.ok, `control ${action}: ${r.status}`); return r.status === 204 ? null : r.json() }
const until = async fn => { for (let i = 0; i < 150; i++) { if (await fn()) return; await new Promise(resolve => setTimeout(resolve, 100)) } throw new Error('fact timeout') }
const ok = text => { checks.push(text); console.log('PASS ' + text) }
try {
  phase = 'normal list/chat and task isolation'
  await page.goto(chat)
  await page.getByRole('textbox', { name: '消息', exact: true }).waitFor()
  await page.getByRole('region', { name: 'sleep 等待' }).first().waitFor()
  assert.ok((await status()).sleep.id === f.firstSleepId)
  assert.equal(await page.getByRole('button', { name: '排队发送', exact: true }).count(), 0)
  assert.ok(await page.getByRole('button', { name: '发送', exact: true }).isEnabled())
  await page.screenshot({ path: new URL('sleep-normal-desktop.png', dir).pathname })
  await page.setViewportSize({ width: 390, height: 1800 })
  await page.screenshot({ path: new URL('sleep-normal-mobile-expanded.png', dir).pathname })
  await taskPage.goto(origin + '/')
  await taskPage.getByRole('region', { name: 'sleep 等待' }).first().waitFor()
  await taskPage.screenshot({ path: new URL('sleep-overview-desktop.png', dir).pathname })
  await taskPage.goto(task(f.nodeTaskId))
  await taskPage.getByRole('region', { name: 'sleep 等待' }).first().waitFor()
  const node = await facts(f.nodeTaskId)
  assert.equal(node.task.status, 'waiting'); assert.equal(node.nodes[0].sleep.state, 'sleeping')
  assert.equal(await taskPage.getByRole('region', { name: '任务内普通对话 sleep' }).count(), 0)
  await taskPage.screenshot({ path: new URL('sleep-task-node-desktop.png', dir).pathname })
  await taskPage.setViewportSize({ width: 390, height: 1800 })
  await taskPage.screenshot({ path: new URL('sleep-task-node-mobile-expanded.png', dir).pathname })
  await taskPage.setViewportSize({ width: 1440, height: 1000 })
  await taskPage.goto(task(f.ordinaryTaskId))
  await taskPage.getByRole('region', { name: '任务内普通对话 sleep' }).waitFor()
  const ordinary = await facts(f.ordinaryTaskId)
  assert.equal(ordinary.task.status, 'blocked'); assert.equal(ordinary.nodes[0].sleep, null)
  assert.equal((await status()).sleep.id, f.firstSleepId)
  await taskPage.setViewportSize({ width: 390, height: 1800 })
  await taskPage.screenshot({ path: new URL('sleep-task-conversation-mobile-expanded.png', dir).pathname })
  ok('actual React normal idle/direct-send UI and waiting node versus blocked ordinary task sleep stay isolated')

  phase = 'React direct send wakes normal early'
  const before = forwarded.flat().filter(r => r.method === 'POST' && r.path.endsWith('/messages')).length
  const sent = page.waitForRequest(r => r.method() === 'POST' && new URL(r.url()).pathname.endsWith(`/agents/${f.agentId}/messages`))
  await page.getByRole('textbox', { name: '消息', exact: true }).fill('actual console early wake')
  await page.getByRole('button', { name: '发送', exact: true }).click()
  const request = await sent
  assert.deepEqual(request.postDataJSON(), { text: 'actual console early wake' })
  await until(async () => { const s = await status(); return !s.busy && s.sleep === null })
  await page.getByText('actual console early wake', { exact: false }).first().waitFor()
  assert.equal(forwarded.flat().filter(r => r.method === 'POST' && r.path.endsWith('/messages')).length, before + 1)
  await page.waitForFunction(id => window.__sleepEvents.some(e => e.scopeKind === 'normal' && e.endedSleepId === id && e.sleepId === null), f.firstSleepId)
  assert.equal((await facts(f.ordinaryTaskId)).sleepingInstances.length, 1)
  ok('actual React direct send makes one POST without whenBusy; real sleep.updated clears normal only')

  phase = 'new sleep and deadline wake platform source'
  const next = await control('normal-sleep')
  await page.waitForFunction(id => window.__sleepEvents.some(e => e.sleepId === id), next.id)
  await page.getByRole('region', { name: 'sleep 等待' }).first().waitFor()
  assert.ok(next.revision > 2)
  await page.reload()
  await page.getByRole('region', { name: 'sleep 等待' }).first().waitFor()
  assert.equal((await status()).sleep.wakeAt, next.wakeAt)
  await control('normal-deadline')
  await until(async () => { const s = await status(); return !s.busy && !s.sleep })
  await page.getByText(/平台 sleep 唤醒/).first().waitFor()
  await page.getByText(`唤醒关联 ${next.id}`, { exact: true }).waitFor()
  assert.equal((await facts(f.nodeTaskId)).task.status, 'waiting')
  await page.setViewportSize({ width: 1440, height: 1000 })
  await page.screenshot({ path: new URL('sleep-wake-source-desktop.png', dir).pathname })
  ok('actual new sleep revision and reload retain wakeAt; real timer message has platform sleep_wake source/reference')

  phase = 'task sleep event with commit version'
  await taskPage.goto(task(f.nodeTaskId))
  await taskPage.getByRole('region', { name: 'sleep 等待' }).first().waitFor()
  await control('node-wake')
  await taskPage.waitForFunction(({ id, version }) => window.__sleepEvents.some(e => e.scopeKind === 'task' && e.taskId === id && e.taskVersion > version && e.endedSleepId && e.sleepId === null), { id: f.nodeTaskId, version: node.task.version })
  await until(async () => (await facts(f.nodeTaskId)).sleepingInstances.length === 0)
  assert.equal((await facts(f.ordinaryTaskId)).sleepingInstances.length, 1)
  assert.equal((await status()).sleep, null)
  const awakened = await facts(f.nodeTaskId)
  assert.equal(awakened.nodes[0].sleep, null)
  assert.notEqual(awakened.task.status, 'completed')
  const taskMessages = await (await fetch(`${f.apiBase}/tasks/${f.nodeTaskId}/nodes/${awakened.nodes[0].id}/messages`)).json()
  assert.ok(taskMessages.messages.some(m => m.role === 'user' && m.origin === 'inbox_reminder' && m.wakeSleepId === node.nodes[0].sleep.id))
  ok('actual task sleep.updated carries committed taskVersion; Inbox work preserves origin and never completes workflow')

  phase = 'real busy queue'
  await control('normal-busy')
  await page.getByRole('button', { name: '排队发送', exact: true }).waitFor()
  await page.getByRole('textbox', { name: '消息', exact: true }).fill('actual queued console origin')
  const queued = page.waitForRequest(r => r.method() === 'POST' && new URL(r.url()).pathname.endsWith(`/agents/${f.agentId}/messages`))
  await page.getByRole('button', { name: '排队发送', exact: true }).click()
  assert.deepEqual((await queued).postDataJSON(), { text: 'actual queued console origin', whenBusy: 'queue' })
  await until(async () => !(await status()).busy)
  const response = await fetch(`${f.apiBase}/agents/${f.agentId}/messages`), history = await response.json()
  assert.ok(history.messages.some(m => m.text === 'actual console early wake' && m.origin === 'console'))
  assert.ok(history.messages.some(m => m.text === 'actual queued console origin' && m.origin === 'console'))
  assert.ok(history.messages.some(m => m.origin === 'sleep_wake' && m.run?.wakeSleepId === next.id))
  ok('actual busy queue retains console origin; sleep_wake alone has platform origin and matching wakeSleepId')
  assert.deepEqual(errors, []); assert.deepEqual(blocked, [])
  writeFileSync(new URL('sleep-app-browser-results.json', dir), JSON.stringify({ mode: 'actual React -> actual temporary App -> TEST PG/Redis + fakepi; HTTP route.fetch and real SSE; not live model', checks, forwarded: forwarded.flat(), events: [...await page.evaluate(() => window.__sleepEvents), ...await taskPage.evaluate(() => window.__sleepEvents)], errors, blocked, passed: true }, null, 2))
} catch (error) {
  writeFileSync(new URL('sleep-app-browser-failure.json', dir), JSON.stringify({ phase, message: error.message, errors, blocked, history: await (await fetch(`${f.apiBase}/agents/${f.agentId}/messages`)).json(), forwarded: forwarded.flat() }, null, 2))
  await page.screenshot({ path: new URL('sleep-app-failure.png', dir).pathname }).catch(() => {})
  throw new Error('Sleep browser phase failed: ' + phase, { cause: error })
} finally { await browser.close() }
