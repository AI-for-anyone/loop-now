/* global window, document */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync } from 'node:fs'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'
const f = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_SLEEP_RECONNECT_MANIFEST, 'utf8'))
assert.ok(['ordinary', 'race'].includes(f.phase))
const dir = new URL(`../test-results/add-agent-sleep/reconnect/${f.phase}/`, import.meta.url)
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const errors = [], blocked = [], checks = [], dimensions = []
let phase = 'initial actual snapshots'
await context.addInitScript(() => {
  window.__sleepReconnectSources = []
  const Actual = window.EventSource
  window.EventSource = class extends Actual {
    constructor(...args) {
      super(...args)
      const source = { path: new URL(args[0]).pathname, opens: 0, errors: 0, events: [] }
      window.__sleepReconnectSources.push(source)
      this.addEventListener('open', () => source.opens++)
      this.addEventListener('error', () => source.errors++)
      for (const type of ['snapshot', 'sleep.updated', 'message.created', 'run.updated', 'run.text', 'run.tool', 'run.ended', 'session.updated', 'task.updated', 'node.updated', 'artifact.published']) {
        this.addEventListener(type, e => {
          const data = JSON.parse(e.data)
          source.events.push({ type, id: e.lastEventId, sleepId: data.sleep?.id ?? null, sleepRevision: data.sleepRevision ?? null, endedSleepId: data.endedSleepId ?? null, taskVersion: data.taskVersion ?? null, messageId: data.message?.id ?? (type === 'message.created' ? data.id : null) })
        })
      }
    }
  }
})
await context.route('**/*', route => {
  const u = new URL(route.request().url())
  if (u.origin === 'http://localhost:5173') return route.continue()
  blocked.push({ method: route.request().method(), host: u.hostname, path: u.pathname })
  return route.abort()
})
const normal = await context.newPage(), task = await context.newPage(), forwarded = []
for (const page of [normal, task]) {
  page.on('pageerror', e => errors.push({ name: e.name, message: e.message }))
  forwarded.push(await forwardTemporaryAPI(page, f.apiBase))
}
const normalPath = `/api/v1/agents/${f.agentId}/events`, taskPath = `/api/v1/tasks/${f.taskId}/events`
const base = `http://localhost:5173/p/${encodeURIComponent(f.projectName)}`
const wire = (p, path) => p.evaluate(path => window.__sleepReconnectSources.filter(s => s.path === path && s.opens > 0), path)
const waitWire = (p, path, opens) => p.waitForFunction(({ path, opens }) => window.__sleepReconnectSources.some(s => s.path === path && s.opens >= opens && s.events.some(e => e.type === 'snapshot')), { path, opens })
const requestLog = async () => (await fetch(f.controlBase + '/requests')).json()
const control = async path => { const r = await fetch(f.controlBase + path, { method: 'POST' }); assert.ok(r.ok, `control ${path}: ${r.status}`); return r.status === 204 ? null : r.json() }
const until = async fn => { for (let i = 0; i < 150; i++) { if (await fn()) return; await new Promise(r => setTimeout(r, 100)) } throw new Error('fact timeout') }
const facts = async path => (await fetch(f.apiBase + path)).json()
const size = async (page, scope, width, height) => {
  await page.setViewportSize({ width, height })
  const d = await page.evaluate(() => ({ innerWidth: window.innerWidth, rootWidth: document.documentElement.scrollWidth, bodyWidth: document.body.scrollWidth }))
  dimensions.push({ scope, width, height, ...d })
  assert.ok(d.rootWidth <= width + 1 && d.bodyWidth <= width + 1, `${scope} root horizontal overflow: ${JSON.stringify(d)}`)
}
const ok = s => { checks.push(s); console.log('PASS ' + s) }
try {
  await normal.goto(`${base}/agents/${f.agentId}/chat`)
  await task.goto(`${base}/tasks/${f.taskId}`)
  await waitWire(normal, normalPath, 1); await waitWire(task, taskPath, 1)
  await normal.getByRole('region', { name: 'sleep 等待' }).first().waitFor()
  await task.getByRole('region', { name: 'sleep 等待' }).first().waitFor()
  for (const [page, scope] of [[normal, 'normal'], [task, 'task']]) {
    await size(page, scope, 1440, 1000); await size(page, scope, 390, 1800)
  }
  const n0 = (await wire(normal, normalPath))[0], t0 = (await wire(task, taskPath))[0]
  const cursors = { normal: n0.events.at(-1).id, task: t0.events.at(-1).id }
  assert.match(cursors.normal, /^\d+-\d+$/); assert.match(cursors.task, /^\d+$/)
  phase = 'native EventSource cursor reconnect with unchanged sleep'
  await control('/cut?scope=both')
  await until(async () => { const r = await requestLog(); return r.some(x => x.path === normalPath && x.lastEventId === cursors.normal) && r.some(x => x.path === taskPath && x.lastEventId === cursors.task) })
  await control('/release')
  await waitWire(normal, normalPath, 2); await waitWire(task, taskPath, 2)
  const n1 = await facts(`/agents/${f.agentId}/status`), t1 = await facts(`/tasks/${f.taskId}`)
  assert.equal(n1.sleep.id, f.normalSleep.id); assert.equal(n1.sleepRevision, f.normalSleep.revision); assert.equal(n1.sleep.wakeAt, f.normalSleep.wakeAt)
  assert.equal(t1.nodes[0].sleep.id, f.nodeSleep.id); assert.equal(t1.nodes[0].sleepRevision, f.nodeSleep.revision); assert.equal(t1.nodes[0].sleep.wakeAt, f.nodeSleep.wakeAt)
  assert.equal((await wire(normal, normalPath))[0].events.filter(e => e.type === 'snapshot').length, 1)
  assert.equal((await wire(task, taskPath))[0].events.filter(e => e.type === 'snapshot').length, 1)
  ok('real native EventSource reconnect sends normal/task Last-Event-ID; effective cursor resumes without a new snapshot; sleep ID/revision/wakeAt unchanged')

  phase = 'missed old terminal and new sleep replay once'
  const old = (await wire(normal, normalPath))[0], checkpoint = old.events.at(-1).id, before = old.events.length
  await control('/cut')
  await until(async () => (await requestLog()).filter(x => x.path === normalPath && x.lastEventId === checkpoint).length >= 2)
  const next = await control('/replace-normal')
  assert.notEqual(next.id, f.normalSleep.id); assert.ok(next.revision > f.normalSleep.revision)
  await control('/release')
  await waitWire(normal, normalPath, 3)
  await normal.waitForFunction(id => window.__sleepReconnectSources.some(s => s.events.some(e => e.type === 'sleep.updated' && e.sleepId === id)), next.id)
  await until(async () => (await normal.locator(`time[datetime="${next.wakeAt}"]`).count()) > 0)
  const n2 = (await wire(normal, normalPath))[0], replay = n2.events.slice(before)
  assert.equal(n2.events.filter(e => e.type === 'snapshot').length, 1)
  assert.equal(replay.filter(e => e.type === 'sleep.updated' && e.endedSleepId === f.normalSleep.id).length, 1)
  assert.equal(replay.filter(e => e.type === 'sleep.updated' && e.sleepId === next.id).length, 1)
  const ids = n2.events.filter(e => e.type !== 'snapshot').map(e => e.id)
  assert.equal(new Set(ids).size, ids.length)
  const runIds = await normal.locator('[data-run-id]').evaluateAll(es => es.map(e => e.dataset.runId))
  assert.equal(new Set(runIds).size, runIds.length)
  assert.equal((await wire(normal, normalPath)).length, 1); assert.equal((await wire(task, taskPath)).length, 1)
  const finalNormal = await facts(`/agents/${f.agentId}/status`), finalTask = await facts(`/tasks/${f.taskId}`)
  assert.equal(finalNormal.sleep.id, next.id); assert.equal(finalNormal.sleepRevision, next.revision); assert.equal(finalNormal.sleep.wakeAt, next.wakeAt)
  assert.equal(finalTask.nodes[0].sleep.id, f.nodeSleep.id); assert.equal(finalTask.nodes[0].sleep.wakeAt, f.nodeSleep.wakeAt)
  ok('real retained events replay old terminal then newer sleep exactly once; new sleep remains; one opened native source per scope and no duplicate rendered run')
  ok('desktop 1440x1000 and mobile expanded 390x1800 normal/task roots have no horizontal overflow')
  assert.deepEqual(errors, []); assert.deepEqual(blocked, [])
  writeFileSync(new URL('sleep-reconnect-browser-results.json', dir), JSON.stringify({ mode: 'actual React -> transparent SSE EOF fault proxy -> real isolated App/PG/Redis+fakepi; no fake events', checks, dimensions, cursors, checkpoint, actualRequests: await requestLog(), normalSources: await wire(normal, normalPath), taskSources: await wire(task, taskPath), allConstructedSources: { normal: await normal.evaluate(() => window.__sleepReconnectSources), task: await task.evaluate(() => window.__sleepReconnectSources) }, replay, forwarded: forwarded.flat(), errors, blocked, passed: true }, null, 2))
} catch (e) {
  writeFileSync(new URL('sleep-reconnect-browser-failure.json', dir), JSON.stringify({ phase, message: e.message, dimensions, actualRequests: await requestLog(), normalSources: await wire(normal, normalPath), taskSources: await wire(task, taskPath), errors, blocked }, null, 2))
  throw new Error('Actual reconnect slice failed: ' + phase, { cause: e })
} finally { await browser.close() }
