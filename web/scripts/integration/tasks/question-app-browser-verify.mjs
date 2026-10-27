/* global window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from '../codex-auth/app-forward.mjs'
const f = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_TASK_QUESTIONS_MANIFEST, 'utf8'))
const dir = fileURLToPath(new URL('.', import.meta.url)), checks = [], errors = [], blocked = [], streams = [], auxiliary = []
let phase = 'initialize', forwarded = [], releases = []
const browser = await chromium.launch({ headless: true }), context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
await context.addInitScript(() => {
  window.__questionEvents = []
  const ActualEventSource = window.EventSource
  window.EventSource = class extends ActualEventSource {
    constructor(...args) {
      super(...args)
      this.addEventListener('inbox.question.updated', event => {
        const value = JSON.parse(event.data)
        window.__questionEvents.push({ type: 'inbox.question.updated', messageId: value.message.id, state: value.message.question?.state })
      })
    }
  }
})
await context.route('**/*', async route => {
  const request = route.request(), url = new URL(request.url())
  if (url.origin === 'http://localhost:5173') return route.continue()
  blocked.push({ method: request.method(), host: url.hostname, path: url.pathname }); return route.abort()
})
const page = await context.newPage(), stale = await context.newPage()
for (const p of [page, stale]) { p.on('pageerror', error => errors.push(error.name)); forwarded.push(await forwardTemporaryAPI(p, f.apiBase)) }
const taskPath = id => `http://localhost:5173/p/${encodeURIComponent(f.projectName)}/tasks/${id}`
const questionPath = (task, thread, message) => `http://localhost:5173/inbox/${thread}?task=${task}&message=${message}`
const facts = async id => { const r = await fetch(f.apiBase + '/tasks/' + id); assert.equal(r.status, 200); return r.json() }
const ok = name => { checks.push(name); console.log('PASS ' + name) }
const post = async (p, path, body) => {
  const value = await p.evaluate(async ({ path, body }) => {
    const r = await fetch('http://127.0.0.1:8780/api/v1' + path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    return { status: r.status, body: await r.json() }
  }, { path, body })
  auxiliary.push({ method: 'POST', path, status: value.status, mode: 'actual browser fetch through same temporary App transport; not React control' }); return value
}
const holdReads = async (p, taskId, threadId) => {
  let release
  const gate = new Promise(resolve => { release = resolve })
  const handler = async route => {
    const req = route.request(), path = new URL(req.url()).pathname
    if (req.method() === 'GET' && (path === `/api/v1/tasks/${taskId}` || path === `/api/v1/inbox/threads/${threadId}`)) await gate
    await route.fallback()
  }
  await p.route('http://127.0.0.1:8780/api/v1/**', handler)
  const done = async () => { release(); await p.unroute('http://127.0.0.1:8780/api/v1/**', handler) }
  releases.push(done); return done
}
try {
  phase = 'multi-node source mapping and task question link'
  await page.goto(taskPath(f.mappedTaskId))
  await page.getByText('Web真实原题：是否继续复核？', { exact: true }).waitFor()
  await page.getByText(/mapped actual source input/).first().waitFor()
  const mapped = await facts(f.mappedTaskId), review = mapped.nodes.at(-1)
  assert.equal(mapped.workflow.steps.length, 2)
  assert.equal(review.inputs.text, 'mapped actual source input')
  assert.equal(review.inputVersions[0].nodeRunId, f.sourceNodeId)
  assert.equal(await page.getByRole('list', { name: '节点列表' }).getByRole('button').count(), 2)
  await page.getByRole('link', { name: '前往回答', exact: true }).click()
  await page.getByRole('radio', { name: '继续', exact: true }).waitFor()
  assert.equal(new URL(page.url()).searchParams.get('message'), f.mappedQuestionId)
  await page.getByText('Web真实原题之后的普通消息', { exact: true }).last().waitFor()
  ok('actual two-node graph and source mapping; task link targets original question despite later ordinary message')
  phase = 'option answer and real question event'
  await page.getByRole('radio', { name: '继续', exact: true }).click()
  await page.getByRole('button', { name: '回答原问题', exact: true }).click()
  await page.getByRole('button', { name: '回答原问题', exact: true }).waitFor({ state: 'hidden' })
  await page.getByText('已保存的回答', { exact: true }).waitFor()
  await page.waitForFunction(id => window.__questionEvents.some(e => e.messageId === id && e.state === 'answered'), f.mappedQuestionId)
  streams.push(...await page.evaluate(() => window.__questionEvents))
  const answered = await facts(f.mappedTaskId)
  assert.equal(answered.nodes.at(-1).question.status, 'answered')
  await page.setViewportSize({ width: 390, height: 1800 })
  await page.locator('#inbox-message-' + f.mappedQuestionId).scrollIntoViewIfNeeded()
  await page.screenshot({ path: dir + 'question-app-answered-mobile.png' })
  const duplicate = await post(page, `/inbox/messages/${f.mappedQuestionId}/reply`, { optionId: '1' })
  assert.equal(duplicate.status, 409); assert.equal(duplicate.body.error.code, 'already_answered')
  ok('React answers original option once; real inbox.question.updated refreshes answered history; late option is 409')
  phase = 'retry closes old question and new question stays open'
  await stale.goto(questionPath(f.retryTaskId, f.oldThreadId, f.oldQuestionId))
  await stale.getByRole('radio', { name: '继续', exact: true }).waitFor()
  const releaseOld = await holdReads(stale, f.retryTaskId, f.oldThreadId)
  await page.setViewportSize({ width: 1440, height: 1000 }); await page.goto(taskPath(f.retryTaskId))
  await page.getByRole('button', { name: '重试当前节点', exact: true }).click()
  await page.getByLabel('重试说明（可选）').fill('actual question retry')
  await page.getByRole('alertdialog').getByRole('button', { name: '重试当前节点', exact: true }).click()
  await page.getByRole('alertdialog').waitFor({ state: 'hidden' })
  const qResponse = await fetch(f.controlBase + '/new-question', { method: 'POST' })
  assert.equal(qResponse.status, 200); const next = await qResponse.json()
  await stale.getByRole('radio', { name: '继续', exact: true }).click()
  const oldRejected = stale.waitForResponse(r => r.request().method() === 'POST' && r.url().endsWith('/'+f.oldQuestionId+'/reply') && r.status() === 409)
  await stale.getByRole('button', { name: '回答原问题', exact: true }).click()
  await oldRejected
  assert.equal(await stale.getByRole('button', { name: '回答原问题', exact: true }).isDisabled(), true)
  await releaseOld()
  await stale.getByText('已关闭', { exact: true }).first().waitFor()
  let current = await facts(f.retryTaskId)
  assert.equal(current.nodes.at(-1).question.id, next.id); assert.equal(current.nodes.at(-1).question.status, 'open')
  assert.equal(current.nodes[0].question.status, 'closed')
  ok('React retry closes old original; stale React option POST rejects once without fallback or answering new question')
  phase = 'cancel and stale first text saved as ordinary'
  await stale.goto(questionPath(f.retryTaskId, next.threadId, next.id))
  await stale.getByRole('textbox', { name: '补充回答', exact: true }).fill('actual late ordinary text')
  const releaseNext = await holdReads(stale, f.retryTaskId, next.threadId)
  await page.getByRole('button', { name: '取消任务', exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '取消任务', exact: true }).click()
  await page.getByRole('alertdialog').waitFor({ state: 'hidden' })
  await stale.getByRole('button', { name: '回答原问题', exact: true }).click()
  await stale.getByText('已保存为普通留言，未记为问题答案。', { exact: true }).first().waitFor()
  await releaseNext()
  await stale.getByText('已关闭', { exact: true }).first().waitFor()
  current = await facts(f.retryTaskId)
  assert.equal(current.task.status, 'cancelled'); assert.equal(current.nodes.at(-1).question.status, 'closed')
  const late = await post(stale, `/inbox/messages/${next.id}/reply`, { optionId: '1' })
  assert.equal(late.status, 409); assert.equal(late.body.error.code, 'question_closed')
  await stale.setViewportSize({ width: 390, height: 1800 }); await stale.screenshot({ path: dir + 'question-app-late-text-mobile.png' })
  ok('React cancel closes new question; one stale text reply is labelled ordinary, never an answer; task stays cancelled')
  phase = 'console permission and event audit'
  await page.goto(taskPath(f.privateTaskId))
  await page.getByText('Web外部身份专属原题', { exact: true }).waitFor()
  assert.equal(await page.getByRole('link', { name: '前往回答', exact: true }).count(), 0)
  const privateFact = await facts(f.privateTaskId)
  assert.equal(privateFact.nodes[0].question.canAnswer, false)
  const denied = await post(page, `/inbox/messages/${f.privateQuestionId}/reply`, { optionId: '1' })
  assert.equal(denied.status, 404)
  const all = forwarded.flat(), replies = id => all.filter(r => r.method === 'POST' && r.path === `/api/v1/inbox/messages/${id}/reply`)
  assert.equal(replies(f.mappedQuestionId).filter(r => r.status === 201).length, 1)
  assert.equal(replies(f.oldQuestionId).length, 1); assert.equal(replies(f.oldQuestionId)[0].status, 409)
  assert.equal(replies(next.id).filter(r => r.status === 201).length, 1)
  assert.equal(all.filter(r => r.method === 'POST' && r.path.endsWith('/retry')).length, 1)
  assert.equal(all.filter(r => r.method === 'POST' && r.path.endsWith('/cancel')).length, 1)
  streams.push(...await stale.evaluate(() => window.__questionEvents))
  assert.equal(streams.some(e => e.messageId === next.id && e.state === 'closed'), true)
  assert.deepEqual(blocked, []); assert.deepEqual(errors, [])
  ok('actual console permission denies foreign question; real terminal events and single-POST/no-fallback audit pass')
  writeFileSync(dir + 'question-app-browser-results.json', JSON.stringify({ mode: 'actual React + explicitly marked browser fetch -> forwarded temporary App -> TEST PG; fakepi; real SSE; selected GETs delayed for stale view without response stubs', checks, forwarded: all, auxiliary, streams, blocked, errors }, null, 2))
} catch (error) {
  writeFileSync(dir + 'question-app-browser-failure.json', JSON.stringify({ phase, errorType: error.name, checks, forwarded: forwarded.flat(), auxiliary, streams, blocked, errors }, null, 2))
  console.error('Actual question integration failed at: ' + phase); process.exitCode = 1
} finally { for (const release of releases) await release().catch(() => {}); await browser.close() }
