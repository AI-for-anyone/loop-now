/* global document, innerWidth */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from '../codex-auth/app-forward.mjs'

// IDs and the ephemeral API address are supplied by the isolated Go test driver.
// No API response fixtures, provider navigation, or existing service writes.
const fixture = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_TASKS_MANIFEST, 'utf8'))
const dir = fileURLToPath(new URL('.', import.meta.url)), checks = [], errors = [], blocked = []
let phase = 'initialize'
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage()
page.on('pageerror', error => errors.push(error.name))
await context.route('**/*', async route => {
  const request = route.request(), url = new URL(request.url())
  if (url.origin === 'http://localhost:5173') return route.continue()
  blocked.push({ method: request.method(), host: url.hostname, path: url.pathname })
  return route.abort()
})
const forwarded = await forwardTemporaryAPI(page, fixture.apiBase)
const listPath = `http://localhost:5173/p/${encodeURIComponent(fixture.projectName)}/tasks`
const ok = name => { checks.push(name); console.log('PASS ' + name) }
const facts = async id => {
  const response = await fetch(fixture.apiBase + '/tasks/' + id)
  assert.equal(response.status, 200)
  return response.json()
}
try {
  phase = 'actual task list'
  await page.goto(listPath)
  await page.getByRole('link', { name: fixture.archivedTitle, exact: true }).waitFor()
  await page.getByRole('link', { name: fixture.blockedTitle, exact: true }).waitFor()
  ok('actual App project list contains isolated completed and blocked fakepi tasks')
  phase = 'archived task records and artifact'
  await page.getByRole('link', { name: fixture.archivedTitle, exact: true }).click()
  await page.getByText('记录已归档，副本已回收', { exact: true }).waitFor()
  await page.getByRole('heading', { name: '实际输入', exact: true }).waitFor()
  await page.getByRole('heading', { name: '输出', exact: true }).waitFor()
  await page.getByRole('region', { name: '节点会话', exact: true }).getByText('只读的任务节点会话', { exact: true }).waitFor()
  const completed = await facts(fixture.archivedTaskId)
  assert.equal(completed.cleanup, 'archived')
  assert.equal(completed.nodes[0].status, 'completed')
  assert.equal(completed.nodes[0].output.file.artifactId, fixture.artifactId)
  const downloadPromise = page.waitForEvent('download')
  await page.getByRole('link', { name: '下载', exact: true }).first().click()
  const download = await downloadPromise
  assert.equal(download.suggestedFilename(), 'web-retained.txt')
  assert.equal(readFileSync(await download.path(), 'utf8'), 'web isolated retained artifact')
  await page.screenshot({ path: dir + 'app-archived-desktop.png' })
  ok('actual archived task exposes fixed graph, record IO, scoped conversation and retained artifact download')
  phase = 'blocked task retry'
  await page.goto(listPath + '/' + fixture.blockedTaskId)
  const retry = page.getByRole('button', { name: '重试当前节点', exact: true })
  await retry.waitFor()
  await page.waitForFunction(() => [...document.querySelectorAll('button')].some(b => b.textContent === '重试当前节点' && !b.disabled))
  const before = await facts(fixture.blockedTaskId), selected = new URL(page.url()).searchParams.get('record')
  assert.equal(before.task.status, 'blocked')
  await retry.click()
  await page.getByLabel('重试说明（可选）').fill('Web isolated retry note')
  const dialog = page.getByRole('alertdialog')
  await dialog.getByRole('button', { name: '重试当前节点', exact: true }).click()
  await dialog.waitFor({ state: 'hidden' })
  await page.getByText('正在查看历史记录', { exact: true }).waitFor()
  assert.equal(new URL(page.url()).searchParams.get('record'), selected)
  const after = await facts(fixture.blockedTaskId)
  assert.notEqual(after.task.currentNodeRunId, before.task.currentNodeRunId)
  assert.equal(after.nodes.length, before.nodes.length + 1)
  assert.equal(after.nodes.at(-1).runtimeId, before.nodes.at(-1).runtimeId)
  ok('real browser retry POST creates new attempt with same runtime; historical record stays selected')
  phase = 'cancel and cleanup'
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: '取消任务', exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '取消任务', exact: true }).click()
  await page.getByRole('alertdialog').waitFor({ state: 'hidden' })
  await page.getByText('记录已归档，副本已回收', { exact: true }).waitFor()
  const cancelled = await facts(fixture.blockedTaskId)
  assert.equal(cancelled.task.status, 'cancelled')
  assert.equal(cancelled.cleanup, 'archived')
  assert.equal(await page.getByRole('button', { name: '取消任务', exact: true }).count(), 0)
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false)
  await page.screenshot({ path: dir + 'app-cancelled-mobile.png' })
  ok('real cancel endpoint reaches cancelled and archived; 390px page remains readable')
  assert.equal(forwarded.filter(r => r.method === 'POST' && r.path.endsWith('/retry') && r.status === 200).length, 1)
  assert.equal(forwarded.filter(r => r.method === 'POST' && r.path.endsWith('/cancel') && r.status === 200).length, 1)
  assert.deepEqual(blocked, [])
  assert.deepEqual(errors, [])
  ok('actual forwarded responses audited; no supplier requests, fixture responses or browser exceptions')
  writeFileSync(dir + 'app-browser-results.json', JSON.stringify({ mode: 'actual React -> route.fetch/actual SSE -> temporary App -> isolated TEST PG; fakepi; no question integration or real model', checks, forwarded, blocked, errors }, null, 2))
} catch (error) {
  writeFileSync(dir + 'app-browser-failure.json', JSON.stringify({ phase, errorType: error.name, checks, forwarded, blocked, errors }, null, 2))
  console.error('Integration failed at phase: ' + phase)
  process.exitCode = 1
} finally { await browser.close() }
