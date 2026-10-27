/* global window, document, DataTransfer, ClipboardEvent, DragEvent, getComputedStyle */
// Actual product React against the server owner's isolated TEST App. No API fixtures.
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { createServer } from '../node_modules/vite/dist/node/index.js'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
const root = fileURLToPath(new URL('../', import.meta.url))
const input = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_IMAGE_MANIFEST, 'utf8'))
const api = new URL(input.apiBase), origin = 'http://127.0.0.1:4344'
assert.equal(api.protocol, 'http:'); assert.equal(api.hostname, '127.0.0.1'); assert.equal(api.pathname, '/api/v1')
assert.ok(!['5173', '8780', '8781', ''].includes(api.port))
assert.ok(!api.search && !api.hash && !api.username && !api.password)
for (const key of ['agentId', 'projectId', 'projectName', 'taskId', 'nodeRunId', 'evidenceDir']) assert.ok(input[key])
const out = input.evidenceDir
mkdirSync(out, { recursive: true })
const checks = [], requests = [], errors = [], denied = []
// A synthetic 2x2 PNG, never a host screenshot or user file.
const png = readFileSync(new URL('../test-fixtures/images/pixel.png', import.meta.url)).toString('base64')
const save = (name, data) => writeFileSync(out + '/' + name, JSON.stringify(data, null, 2), { mode: 0o600 })
let server, browser
try {
  process.env.VITE_LOOP_NOW_API_BASE = api.href
  server = await createServer({ root, server: { host: '127.0.0.1', port: 4344, strictPort: true } }); await server.listen()
  browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  await context.route('**/*', route => {
    const request = route.request(), url = new URL(request.url())
    if (url.origin === origin || url.origin === api.origin) return route.continue()
    denied.push({ origin: url.origin, path: url.pathname }); return route.abort()
  })
  await context.addInitScript(() => {
    const Native = window.EventSource
    window.__imageTaskEvents = 0
    window.__imageReferencesOnly = true
    window.__imageReferenceEvents = 0
    window.EventSource = class extends Native { constructor(...args) { super(...args); this.addEventListener('task.inbox.updated', event => { window.__imageTaskEvents++; const images = JSON.parse(event.data).message?.images; if (Array.isArray(images) && images.length) { window.__imageReferenceEvents++; if (images.some(image => Object.keys(image).some(key => !['id', 'mimeType', 'size', 'width', 'height'].includes(key)))) window.__imageReferencesOnly = false } }) } }
  })
  const page = await context.newPage()
  page.on('pageerror', error => errors.push(error.name))
  page.on('response', response => { const url = new URL(response.url()); if (url.origin === api.origin) requests.push({ method: response.request().method(), path: url.pathname, status: response.status() }) })
  const transfer = async (mode, selector, name) => page.evaluate(({ mode, selector, name, png }) => {
    const target = document.querySelector(selector), data = new DataTransfer()
    data.items.add(new File([Uint8Array.from(atob(png), value => value.charCodeAt(0))], name, { type: 'image/png' }))
    target.dispatchEvent(mode === 'paste' ? new ClipboardEvent('paste', { bubbles: true, cancelable: true, clipboardData: data }) : new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: data }))
  }, { mode, selector, name, png })
  const persistentImages = async expected => {
    const gallery = page.getByRole('list', { name: '消息图片' })
    await gallery.first().waitFor()
    assert.equal(await gallery.locator('img').count(), expected)
    for (const image of await gallery.locator('img').all()) {
      const url = new URL(await image.getAttribute('src'))
      assert.equal(url.origin, api.origin); assert.equal(url.protocol, 'http:'); assert.equal(url.search, '')
      await image.evaluate(image => image.decode())
    }
  }
  const chatPath = `/p/${encodeURIComponent(input.projectName)}/agents/${input.agentId}/chat`
  await page.goto(origin + chatPath)
  await page.getByLabel('选择图片文件').setInputFiles({ name: '选择.png', mimeType: 'image/png', buffer: Buffer.from(png, 'base64') })
  await page.getByRole('button', { name: '放大图片：选择.png' }).click()
  await page.getByRole('dialog').waitFor(); await page.keyboard.press('Escape')
  await page.getByRole('button', { name: '移除图片：选择.png' }).click()
  await transfer('paste', 'textarea', '粘贴.png')
  await page.getByRole('button', { name: '放大图片：粘贴.png' }).waitFor()
  const normalSent = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/api/v1/agents/${input.agentId}/messages`)
  await page.getByRole('button', { name: '发送', exact: true }).click()
  assert.equal((await normalSent).status(), 202)
  await persistentImages(1)
  assert.equal(await page.getByRole('list', { name: '待发送图片' }).count(), 0)
  await page.reload(); await persistentImages(1)
  await page.getByRole('list', { name: '消息图片' }).getByRole('button').first().click(); await page.getByRole('dialog').waitFor(); await page.keyboard.press('Escape')
  assert.equal(await page.locator('.riso-paper').first().evaluate(element => getComputedStyle(element).backgroundImage), 'none')
  await page.screenshot({ path: out + '/normal-images-1440.png', fullPage: true })
  checks.push('file selection/preview/removal and pasted pure PNG; actual normal POST; durable image content reload and zoom; paper dots removed')
  const taskPath = `/p/${encodeURIComponent(input.projectName)}/tasks/${input.taskId}?record=${input.nodeRunId}`
  await page.goto(origin + taskPath)
  const composer = page.locator('[data-node-draft]')
  await composer.getByRole('textbox').fill('合成图片任务消息')
  await composer.getByLabel('选择图片文件').setInputFiles({ name: '节点选择.png', mimeType: 'image/png', buffer: Buffer.from(png, 'base64') })
  await transfer('drop', '[data-node-draft] textarea', '节点拖入.png')
  await composer.getByRole('button', { name: '放大图片：节点拖入.png' }).waitFor()
  const nodeSent = page.waitForResponse(response => response.request().method() === 'POST' && new URL(response.url()).pathname === `/api/v1/tasks/${input.taskId}/nodes/${input.nodeRunId}/inbox-messages`)
  await composer.getByRole('button', { name: '发送', exact: true }).click()
  assert.equal((await nodeSent).status(), 201)
  await persistentImages(2)
  await page.waitForFunction(() => window.__imageTaskEvents > 0 && window.__imageReferenceEvents > 0)
  assert.equal(await page.evaluate(() => window.__imageReferencesOnly), true)
  assert.equal(await composer.getByRole('textbox').inputValue(), '')
  await page.reload(); await persistentImages(2)
  const taskImage = page.getByRole('list', { name: '消息图片' }).locator('img').first()
  await taskImage.scrollIntoViewIfNeeded()
  const visible = await taskImage.evaluate(image => {
    const scroll = image.closest('[data-conversation-scroll]'), frame = scroll.getBoundingClientRect(), rect = image.getBoundingClientRect()
    return { scrollHeight: frame.height, imageVisible: rect.top >= frame.top && rect.bottom <= frame.bottom && rect.left >= frame.left && rect.right <= frame.right }
  })
  assert.ok(visible.scrollHeight >= 100); assert.equal(visible.imageVisible, true)
  const normalPosts = requests.filter(request => request.method === 'POST' && request.path === `/api/v1/agents/${input.agentId}/messages`)
  const taskPosts = requests.filter(request => request.method === 'POST' && request.path.endsWith('/inbox-messages'))
  assert.equal(normalPosts.length, 1); assert.equal(taskPosts.length, 1)
  assert.deepEqual(denied, []); assert.deepEqual(errors, [])
  assert.ok(requests.every(request => request.status < 400))
  await page.screenshot({ path: out + '/task-images-1440.png', fullPage: true })
  checks.push('text plus selected/dropped two PNGs; one strict task Inbox POST without normal fallback; real task SSE and durable history reload')
  save('browser-results.json', { passed: true, boundary: 'real React -> isolated temporary App HTTP/SSE; synthetic PNGs; server harness owns PG/fakePi image payload assertions and cleanup; no real model/provider/OAuth/QA', checks, requests, errors, denied })
  await context.close()
} catch (error) { save('browser-failure.json', { passed: false, errorType: error.name, sourceLine: error.stack?.match(/image-app-browser\.mjs:\d+:\d+/)?.[0] ?? null, checks, requests, errors, denied }); process.exitCode = 1 }
finally { await browser?.close(); await server?.close() }
