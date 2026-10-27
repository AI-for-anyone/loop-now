/* global document, window */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { spawn } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import assert from 'node:assert/strict'
import { forwardTemporaryAPI } from './integration/codex-auth/app-forward.mjs'

const manifest = JSON.parse(readFileSync(process.env.LOOP_NOW_WEB_PROJECT_CREDENTIALS_MANIFEST, 'utf8'))
const api = new URL(manifest.apiBase), web = new URL(manifest.webOrigin)
assert.equal(api.hostname, '127.0.0.1'); assert.equal(api.protocol, 'http:'); assert.equal(api.pathname, '/api/v1')
assert.ok(api.port && !['8780', '8781', '5173'].includes(api.port))
assert.equal(web.hostname, '127.0.0.1'); assert.equal(web.port, '4317')
assert.ok(!api.username && !api.password && !api.search && !api.hash)
assert.ok(['crud', 'delete', 'focus'].includes(manifest.slice))
const out = new URL(`../test-results/add-project-credentials/app-${manifest.slice}/`, import.meta.url)
mkdirSync(out, { recursive: true })
const checks = [], errors = [], blocked = [], raceWrites = [], ids = {}
let browser, preview
const root = fileURLToPath(new URL('../', import.meta.url))
const previewReady = () => new Promise((resolve, reject) => {
  preview = spawn(process.execPath, [root + 'node_modules/vite/bin/vite.js', '--host', '127.0.0.1', '--port', web.port, '--strictPort'], { cwd: root, env: { PATH: process.env.PATH, HOME: process.env.HOME ?? '' }, stdio: ['ignore', 'pipe', 'pipe'] })
  const timeout = setTimeout(() => reject(new Error('temporary frontend readiness timeout')), 10000)
  const receive = buffer => { if (buffer.toString().includes(manifest.webOrigin)) { clearTimeout(timeout); resolve() } }
  preview.stdout.on('data', receive)
  preview.once('exit', code => { clearTimeout(timeout); reject(new Error(`temporary frontend exited ${code}`)) })
  preview.once('error', error => { clearTimeout(timeout); reject(error) })
})
const ok = message => { checks.push(message); console.log('PASS ' + message) }
let forwarded = []
try {
  await previewReady()
  browser = await chromium.launch({ headless: true })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
  await context.route('**/*', route => {
    const url = new URL(route.request().url()), path = url.pathname
    // No model, direct chat, task dispatch or OAuth start in this CRUD window.
    if (route.request().method() !== 'GET' && (/\/messages(?:\/|$)/.test(path) || /\/model-auth\/operations(?:\/|$)/.test(path))) {
      blocked.push({ method: route.request().method(), path, reason: 'outside CRUD window' }); return route.abort()
    }
    if ([web.origin, api.origin, 'http://127.0.0.1:8780'].includes(url.origin)) return route.continue()
    blocked.push({ method: route.request().method(), host: url.hostname, path }); return route.abort()
  })
  const page = await context.newPage()
  page.on('pageerror', e => errors.push(e.message))
  page.on('console', message => { if (message.type() === 'error' && !message.text().startsWith('Failed to load resource:')) errors.push(message.text()) })
  forwarded = await forwardTemporaryAPI(page, manifest.apiBase, web.origin)
  const base = `/p/${encodeURIComponent(manifest.projectName)}`, config = `${base}/agents/${manifest.agentId}/config`
  const accountRegion = () => page.getByRole('region', { name: '模型账号', exact: true })
  const modelRegion = () => page.getByRole('region', { name: '模型账号凭证', exact: true })
  const actual = async (method, path, data, status = 200) => {
    assert.ok(!/\/messages(?:\/|$)|\/model-auth\/operations(?:\/|$)/.test(path))
    const response = await context.request.fetch(manifest.apiBase + path, { method, data, headers: { origin: web.origin }, maxRedirects: 0, maxRetries: 0 })
    assert.equal(response.status(), status, `${method} ${path}: ${response.status()}`)
    return status === 204 ? null : response.json()
  }
  const getSelections = () => actual('GET', `/agents/${manifest.agentId}/credential-selections`)
  const readAccounts = () => actual('GET', `/projects/${manifest.projectId}/credential-accounts`)
  const findModel = state => state.selections.find(item => item.purpose === 'model' && item.provider === manifest.provider)
  const screenshot = async (locator, name) => {
    await locator.screenshot({ path: new URL(name + '.png', out).pathname })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
  }
  const createModelAccount = async (name, synthetic) => {
    await page.goto(web.origin + base + '/settings')
    const region = accountRegion()
    await region.getByRole('option', { name: manifest.provider, exact: true }).waitFor({ state: 'attached' })
    await region.getByLabel('添加模型账号 · 提供方').selectOption(manifest.provider)
    await region.getByRole('button', { name: '录入 API Key', exact: true }).click()
    await region.getByLabel('账号名称', { exact: true }).fill(name)
    await region.getByLabel('API Key', { exact: true }).fill(synthetic)
    await region.getByRole('button', { name: '保存', exact: true }).click()
    await region.getByRole('heading', { name, exact: true }).waitFor()
    await region.getByLabel('API Key', { exact: true }).waitFor({ state: 'hidden' })
    assert.equal(await region.getByLabel('API Key', { exact: true }).count(), 0)
    const data = await readAccounts(), account = data.accounts.find(item => item.name === name)
    assert.ok(account); return account
  }

  if (manifest.slice === 'focus') {
    for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
      await page.setViewportSize(viewport)
      await page.goto(web.origin + base + '/settings')
      const row = accountRegion().getByRole('listitem').filter({ has: page.getByRole('heading', { name: manifest.mainAccountName, exact: true }) })
      const trigger = row.getByRole('button', { name: '删除账号', exact: true })
      await trigger.focus(); await page.keyboard.press('Enter')
      const modal = page.getByRole('alertdialog'), cancelButton = modal.getByRole('button', { name: '取消', exact: true }), confirmButton = modal.getByRole('button', { name: '删除账号', exact: true })
      await cancelButton.waitFor()
      assert.ok(await cancelButton.evaluate(element => element === document.activeElement), 'destructive dialog initially focuses cancel')
      await page.keyboard.press('Tab')
      assert.ok(await confirmButton.evaluate(element => element === document.activeElement), 'Tab reaches confirm')
      await page.keyboard.press('Shift+Tab')
      assert.ok(await cancelButton.evaluate(element => element === document.activeElement), 'Shift+Tab returns cancel')
      await screenshot(modal, `account-cancel-focus-${viewport.width}`)
      await page.keyboard.press('Escape'); await modal.waitFor({ state: 'hidden' })
      assert.ok(await trigger.evaluate(element => element === document.activeElement), 'cancel restores account trigger focus')
      ok(`actual App ${viewport.width}: destructive modal keyboard/Tab/cancel focus restoration`)
      await page.goto(web.origin + `/platform/agents/${manifest.platformAgentId}/config`)
      const keyTrigger = modelRegion().getByRole('button', { name: '替换 API Key', exact: true })
      await keyTrigger.focus(); await page.keyboard.press('Enter')
      const keyDialog = page.getByRole('dialog'), keyInput = keyDialog.getByLabel('API Key', { exact: true })
      await keyInput.waitFor()
      assert.equal(await keyInput.getAttribute('type'), 'password')
      assert.ok(await keyInput.evaluate(element => element === document.activeElement), 'key dialog focuses masked input')
      await page.keyboard.press('Tab')
      assert.ok(await keyDialog.getByRole('button', { name: '取消', exact: true }).evaluate(element => element === document.activeElement), 'key input Tab reaches cancel')
      await screenshot(keyDialog, `platform-key-focus-${viewport.width}`)
      await page.keyboard.press('Escape'); await keyDialog.waitFor({ state: 'hidden' })
      assert.ok(await keyTrigger.evaluate(element => element === document.activeElement), 'key dialog restores trigger focus')
      ok(`actual App ${viewport.width}: platform secret dialog masked input/Tab/cancel focus restoration`)
    }
    assert.equal(forwarded.filter(item => item.method !== 'GET').length, 0)
    ok('focus-only browser issued no writes, no model/OAuth requests')
  } else if (manifest.slice === 'delete') {
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto(web.origin + base + '/settings')
    const row = accountRegion().getByRole('listitem').filter({ has: page.getByRole('heading', { name: manifest.mainAccountName, exact: true }) })
    await row.getByRole('button', { name: '删除账号', exact: true }).click()
    await page.getByRole('alertdialog').getByText(/明确选择/).first().waitFor()
    await screenshot(page.getByRole('alertdialog'), 'account-delete-mobile')
    await page.getByRole('alertdialog').getByRole('button', { name: '删除账号', exact: true }).click()
    await page.getByRole('alertdialog').waitFor({ state: 'hidden' })
    await page.goto(web.origin + config)
    await modelRegion().getByText('明确选择的账号已删除，不自动换用其他账号', { exact: true }).waitFor()
    assert.equal(findModel(await getSelections()).selectedAccountId, manifest.mainAccountId)
    ok('delete-only actual account deletion retains explicit invalid selection; historical version checked by PG')
    await page.goto(web.origin + `/p/${encodeURIComponent(manifest.deleteProjectName)}/settings`)
    await page.getByRole('region', { name: '删除项目', exact: true }).getByRole('button', { name: '删除这个项目', exact: true }).click()
    await screenshot(page.getByRole('alertdialog'), 'project-delete-mobile')
    await page.getByRole('alertdialog').getByRole('button', { name: '删除项目', exact: true }).click()
    await page.waitForURL(url => url.pathname.endsWith('/agents'))
    await actual('GET', `/projects/${manifest.deleteProjectId}/credential-accounts`, undefined, 404)
    ok('delete-only actual project deletion returns default page and rejects deleted project account access')
    await page.goto(web.origin + `/platform/agents/${manifest.platformAgentId}/config`)
    await modelRegion().getByText('已设置', { exact: true }).waitFor()
    assert.equal((await actual('GET', `/agents/${manifest.platformAgentId}/model-auth`)).credential.authMethod, 'api_key')
    await screenshot(modelRegion(), 'platform-preserved-mobile')
    ok('delete-only platform private account unaffected; Go verifies ciphertext and zero run/OAuth counters')
  } else {
  await page.goto(web.origin + config)
  await modelRegion().getByText('未配置对应账号，请去项目设置维护', { exact: true }).waitFor()
  assert.equal(findModel(await getSelections()).status, 'not_configured')
  assert.equal(await page.getByRole('button', { name: '录入 API Key', exact: true }).count(), 0)
  ok('actual React→temporary App: zero accounts, project scope has no private secret form')

  const a = await createModelAccount('浏览器共享账号A（长名称完整展示）', 'synthetic-web-project-key-A')
  ids.accountA = a.id
  await page.goto(web.origin + config)
  await modelRegion().getByText('自动使用唯一对应的项目账号', { exact: true }).waitFor()
  assert.equal(findModel(await getSelections()).resolvedAccountId, a.id)
  await screenshot(modelRegion(), 'one-desktop')
  ok('actual React creates API Key account; one account automatically resolves from server')

  const beforeSecond = await actual('GET', `/projects/${manifest.projectId}/credential-account-impact?purpose=model&provider=${encodeURIComponent(manifest.provider)}`)
  assert.ok(beforeSecond.agents.some(item => item.agentId === manifest.agentId && item.mode === 'automatic'))
  const b = await createModelAccount('浏览器共享账号B', 'synthetic-web-project-key-B')
  ids.accountB = b.id
  await page.goto(web.origin + config)
  await modelRegion().getByText(/多个对应账号，需要明确选择/).waitFor()
  const choose = modelRegion().getByRole('combobox')
  assert.equal(await choose.inputValue(), '')
  assert.equal(findModel(await getSelections()).status, 'selection_required')
  await choose.selectOption(a.id)
  await modelRegion().getByRole('button', { name: '保存选择', exact: true }).click()
  await modelRegion().getByText('明确使用所选项目账号', { exact: true }).waitFor()
  assert.equal(findModel(await getSelections()).selectedAccountId, a.id)
  ok('actual 0/1/2 resolution and explicit selection; impact includes automatic consumers')

  await choose.selectOption(b.id)
  const secondTab = await getSelections()
  await actual('PUT', `/agents/${manifest.agentId}/credential-selections`, { purpose: 'model', provider: manifest.provider, accountId: b.id, expectedConfigVersion: secondTab.configVersion })
  raceWrites.push({ resource: 'agent-selection', expectedVersion: secondTab.configVersion })
  const countBefore = forwarded.filter(item => item.method === 'PUT' && item.path.endsWith('/credential-selections')).length
  await modelRegion().getByRole('button', { name: '保存选择', exact: true }).click()
  const conflict = modelRegion().getByRole('alert').filter({ hasText: '配置已在别处更新，尚未保存本次修改' })
  await conflict.waitFor()
  assert.equal(await choose.inputValue(), b.id)
  assert.equal(forwarded.filter(item => item.method === 'PUT' && item.path.endsWith('/credential-selections')).length, countBefore + 1)
  await conflict.getByRole('button', { name: '已核对，采用此版本', exact: true }).click()
  await modelRegion().getByRole('button', { name: '保存选择', exact: true }).click()
  await modelRegion().getByText('明确使用所选项目账号', { exact: true }).waitFor()
  assert.equal(findModel(await getSelections()).selectedAccountId, b.id)
  ok('actual selection CAS412 keeps draft; single request, explicit review then single-item retry')

  await page.goto(web.origin + base + '/settings')
  const rowA = accountRegion().getByRole('listitem').filter({ has: page.getByRole('heading', { name: a.name, exact: true }) })
  await rowA.getByRole('button', { name: '整体替换凭证', exact: true }).click()
  await rowA.getByLabel('API Key', { exact: true }).fill('synthetic-web-project-key-replacement')
  const latestA = await actual('GET', `/projects/${manifest.projectId}/credential-accounts/${a.id}`)
  await actual('PATCH', `/projects/${manifest.projectId}/credential-accounts/${a.id}`, { name: a.name, expectedRevision: latestA.revision })
  raceWrites.push({ resource: 'account', accountId: a.id, expectedVersion: latestA.revision })
  const replacesBefore = forwarded.filter(item => item.method === 'PUT' && item.path.endsWith('/credential')).length
  await rowA.getByRole('button', { name: '保存', exact: true }).click()
  const accountConflict = rowA.getByRole('alert').filter({ hasText: '配置已在别处更新，尚未保存本次修改' })
  await accountConflict.waitFor()
  assert.equal(await rowA.getByLabel('API Key', { exact: true }).inputValue(), 'synthetic-web-project-key-replacement')
  assert.equal(forwarded.filter(item => item.method === 'PUT' && item.path.endsWith('/credential')).length, replacesBefore + 1)
  await screenshot(accountConflict, 'account-cas-desktop')
  await page.setViewportSize({ width: 390, height: 844 }); await screenshot(accountConflict, 'account-cas-mobile')
  await accountConflict.getByRole('button', { name: '已核对，采用此版本', exact: true }).click()
  await rowA.getByRole('button', { name: '保存', exact: true }).click()
  await rowA.getByLabel('API Key', { exact: true }).waitFor({ state: 'hidden' })
  assert.equal(await rowA.getByLabel('API Key', { exact: true }).count(), 0)
  const updatedA = await actual('GET', `/projects/${manifest.projectId}/credential-accounts/${a.id}`)
  assert.notEqual(updatedA.currentVersionId, a.currentVersionId)
  ok('actual account revision CAS412, masked draft and reviewed replacement produce new version')

  const rowB = accountRegion().getByRole('listitem').filter({ has: page.getByRole('heading', { name: b.name, exact: true }) })
  await rowB.getByRole('button', { name: '删除账号', exact: true }).click()
  const deleteDialog = page.getByRole('alertdialog')
  await deleteDialog.getByText(/明确选择/).first().waitFor()
  await screenshot(deleteDialog, 'account-delete-mobile')
  await deleteDialog.getByRole('button', { name: '删除账号', exact: true }).click()
  await deleteDialog.waitFor({ state: 'hidden' })
  await page.goto(web.origin + config)
  await modelRegion().getByText('明确选择的账号已删除，不自动换用其他账号', { exact: true }).waitFor()
  const deleted = findModel(await getSelections())
  assert.equal(deleted.selectedAccountId, b.id); assert.equal(deleted.status, 'account_deleted')
  assert.ok(deleted.candidates.some(item => item.id === a.id))
  await screenshot(modelRegion(), 'selected-deleted-mobile')
  ok('actual delete CAS and explicit impact; selected deleted account never falls back to remaining A')

  await page.goto(web.origin + base + '/settings')
  const cli = page.getByRole('region', { name: 'CLI 账号', exact: true })
  await cli.getByRole('option', { name: 'gh', exact: true }).waitFor({ state: 'attached' })
  await cli.getByLabel('添加 CLI 账号 · 工具').selectOption('gh')
  await cli.getByRole('button', { name: '按模板录入账号', exact: true }).click()
  await cli.getByLabel('账号名称', { exact: true }).fill('浏览器GitHub共享账号')
  await cli.getByLabel('访问令牌', { exact: false }).fill('synthetic-web-project-gh-token')
  await cli.getByRole('button', { name: '保存', exact: true }).click()
  await cli.getByRole('heading', { name: '浏览器GitHub共享账号', exact: true }).waitFor()
  ids.cliAccount = (await readAccounts()).accounts.find(item => item.cli === 'gh').id
  assert.equal((await actual('GET', `/agents/${manifest.agentId}`)).clis.length, 0)
  await page.goto(web.origin + config)
  const clis = page.getByRole('region', { name: '可用 CLI', exact: true })
  await clis.getByRole('textbox', { name: 'CLI 名称', exact: true }).fill('gh')
  await clis.getByRole('button', { name: '添加 CLI', exact: true }).click()
  await clis.getByRole('listitem', { name: 'gh', exact: true }).getByText('自动使用唯一对应的项目账号', { exact: true }).waitFor()
  ok('actual CLI account save grants no permission; explicit CLI allow-list change enables automatic source')

  await page.goto(web.origin + `/platform/agents/${manifest.platformAgentId}/config`)
  const privateArea = page.getByRole('region', { name: '模型账号凭证', exact: true })
  await privateArea.getByRole('button', { name: '录入 API Key', exact: true }).click()
  const privateDialog = page.getByRole('dialog')
  await privateDialog.getByLabel('API Key', { exact: true }).fill('synthetic-web-platform-private-key')
  await privateDialog.getByRole('button', { name: '保存', exact: true }).click()
  await privateDialog.waitFor({ state: 'hidden' })
  assert.equal((await readAccounts()).accounts.filter(item => item.purpose === 'model').length, 1)
  assert.equal((await actual('GET', `/agents/${manifest.platformAgentId}/model-auth`)).credential.authMethod, 'api_key')
  await screenshot(privateArea, 'platform-private-mobile')
  ok('actual platform private API Key path retained, project accounts unchanged')

  const deleteBase = `/p/${encodeURIComponent(manifest.deleteProjectName)}`
  await page.goto(web.origin + deleteBase + '/settings')
  const danger = page.getByRole('region', { name: '删除项目', exact: true })
  await danger.getByRole('button', { name: '删除这个项目', exact: true }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '删除项目', exact: true }).click()
  await page.waitForURL(url => url.pathname.endsWith('/agents'))
  await actual('GET', `/projects/${manifest.deleteProjectId}/credential-accounts`, undefined, 404)
  ok('actual legal project deletion removes project credential access; isolated PG read-back follows in Go')
  }
  assert.deepEqual(errors, []); assert.deepEqual(blocked, [])
  if (manifest.slice === 'crud') {
    assert.ok(forwarded.some(item => item.status === 412 && item.path.endsWith('/credential-selections')))
    assert.ok(forwarded.some(item => item.status === 412 && item.path.endsWith('/credential')))
  }
  writeFileSync(new URL('browser-results.json', out), JSON.stringify({ mode: 'actual React -> route.fetch -> temporary app.Build -> isolated TEST PostgreSQL/Redis; CRUD only', checks, ids, forwarded, raceWrites, errors, blocked, viewports: ['1440x1000', '390x844'], screenshotBoundary: 'region screenshots may exceed viewport height; no OAuth/model requests' }, null, 2))
} catch (error) {
  writeFileSync(new URL('browser-failure.json', out), JSON.stringify({ errorType: error.name, message: error.message, checks, ids, forwarded, raceWrites, errors, blocked }, null, 2)); throw error
} finally {
  await browser?.close()
  if (preview && preview.exitCode === null) {
    preview.kill('SIGTERM')
    await new Promise(resolve => { const timeout = setTimeout(resolve, 3000); preview.once('exit', () => { clearTimeout(timeout); resolve() }) })
  }
}
