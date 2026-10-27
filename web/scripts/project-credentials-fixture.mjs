/* global window, document */
import { chromium } from '/Users/ljl/.npm/_npx/705bc6b22212b352/node_modules/playwright/index.mjs'
import { mkdirSync, writeFileSync } from 'node:fs'
import assert from 'node:assert/strict'

// 实际React + HTTP fixture；所有API与供应商导航均拦截，不连接当前DEV/数据库/供应商。
const origin = process.env.PROJECT_CREDENTIALS_WEB_ORIGIN ?? 'http://127.0.0.1:4317'
const out = new URL('../test-results/add-project-credentials/fixture/', import.meta.url)
mkdirSync(out, { recursive: true })
const now = '2026-10-15T01:00:00Z', id = suffix => `10000000-0000-7000-8000-${suffix.padStart(12, '0')}`
const project = { id: id('1'), organizationId: id('2'), name: '凭证验证项目', isDefault: false, managerAgentId: null, agentCount: 1, createdAt: now, updatedAt: now }
const defaultProject = { ...project, id: id('3'), name: '默认项目', isDefault: true, agentCount: 0 }
const agent = { id: id('4'), scope: 'project', projectId: project.id, isManager: false, name: '凭证验证Agent', address: null, model: { provider: 'anthropic', modelId: 'claude-fixture' }, prompt: '', promptVersion: 1, configVersion: 1, busy: false, session: { state: 'active', epoch: 1 }, folderSources: [], clis: [{ name: 'gh', hasTemplate: true, credential: null }, { name: 'jq', hasTemplate: false, credential: null }], modelCredential: null, createdAt: now, updatedAt: now }
const platformAgent = { ...agent, id: id('9'), scope: 'platform', projectId: null, name: '平台私有Agent' }
const modelAccount = { id: id('5'), projectId: project.id, purpose: 'model', provider: 'anthropic', cli: null, name: '共享开发账号（长名称换行验证）', accountLabel: null, revision: 1, currentVersionId: id('6'), authMethod: 'api_key', status: 'saved', template: null, createdAt: now, updatedAt: now, deletedAt: null }
const codexAccount = { ...modelAccount, id: id('7'), provider: 'openai-codex', name: 'Codex 共享账号', accountLabel: 'fixture@example.test', authMethod: 'oauth', status: 'logged_in' }
const ghAccount = { ...modelAccount, id: id('8'), purpose: 'cli', provider: null, cli: 'gh', name: 'GitHub 项目账号', authMethod: null, template: { source: 'builtin', credentialFormat: 'env-v1', fieldNames: ['token'], warnings: [], status: 'current' } }
const templates = [{ name: 'gh', version: '2.100.0', fields: [{ name: 'token', label: '访问令牌', type: 'secret', required: true }], injection: 'env', install: { status: 'installed' }, warnings: [] }]
const providerList = { piVersion: '1.1.0', catalogGeneratedAt: null, providers: [{ id: 'anthropic', apiKeyEnvVars: [], modelCount: 1, authMethods: ['api_key'] }, { id: 'openai-codex', apiKeyEnvVars: [], modelCount: 1, authMethods: ['oauth'] }] }
const models = provider => ({ providerId: provider, models: [{ id: provider === 'anthropic' ? 'claude-fixture' : 'codex-fixture', name: 'Fixture model', reasoning: false, contextWindow: 1000, maxTokens: 100, input: ['text'] }] })
const limits = { maxUploadFile: 1000000, maxMessage: 65536, maxInboxMessage: 65536, maxSkillArchive: 1000000, maxSkillEntries: 100, maxCredential: 65536, textEdit: { maxBytes: 1000000, extensions: ['.md', '.txt'] } }
const base = `/p/${encodeURIComponent(project.name)}`
let accounts = [modelAccount, codexAccount, ghAccount], conflict = false
let selection = { agentId: agent.id, projectId: project.id, configVersion: 1, credentialsRevision: 1, selections: [{ purpose: 'model', provider: 'anthropic', cli: null, mode: 'legacy_cleared', selectedAccountId: null, resolvedAccountId: null, resolvedVersionId: null, status: 'not_configured', candidates: [modelAccount] }, { purpose: 'cli', provider: null, cli: 'gh', mode: 'automatic', selectedAccountId: null, resolvedAccountId: ghAccount.id, resolvedVersionId: ghAccount.currentVersionId, status: 'resolved', candidates: [ghAccount] }] }
const browser = await chromium.launch({ headless: true })
const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } })
const page = await context.newPage(), checks = [], errors = [], blocked = [], writes = [], unhandled = [], httpConsole = []
page.on('pageerror', e => errors.push(e.message))
page.on('console', message => { if (message.type() === 'error') (message.text().startsWith('Failed to load resource:') ? httpConsole : errors).push(message.text()) })
const json = (route, value, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value), headers: { 'Access-Control-Allow-Origin': origin } })
await context.route('**/*', async route => {
  const request = route.request(), url = new URL(request.url()), method = request.method()
  if (!url.pathname.startsWith('/api/v1/')) {
    if (url.origin === origin) return route.continue()
    blocked.push({ host: url.hostname, path: url.pathname }); return route.abort()
  }
  const path = url.pathname.slice('/api/v1'.length)
  if (method === 'OPTIONS') return route.fulfill({ status: 204, headers: { 'Access-Control-Allow-Origin': origin, 'Access-Control-Allow-Headers': 'content-type', 'Access-Control-Allow-Methods': 'GET,POST,PUT,PATCH,DELETE' } })
  // 本驱动不验Inbox事件；明确终止默认shell事件流，不能把它当真实SSE或重连证据。
  if (path === '/inbox/events') return route.fulfill({ status: 204, headers: { 'Access-Control-Allow-Origin': origin } })
  if (method !== 'GET') writes.push({ method, path }) // 不记录秘密载荷。
  if (path === '/health') return json(route, { ok: true, postgres: { ok: true, error: null }, redis: { ok: true, error: null } })
  if (path === '/context') return json(route, { organization: { id: project.organizationId, name: '测试组织' }, project: { id: project.id, name: project.name }, limits })
  if (path === '/projects') return json(route, { projects: [defaultProject, project] })
  if (path === '/platform/agents') return json(route, { agents: [{ ...platformAgent, sessionState: 'active', sleep: null, sleepRevision: 0 }] })
  if (path.endsWith('/prompt')) return json(route, { prompt: '', promptVersion: 1 })
  if (/^\/projects\/[^/]+\/agents$/.test(path)) return json(route, { agents: path.includes(project.id) ? [{ ...agent, sessionState: 'active', sleep: null, sleepRevision: 0 }] : [] })
  if (path === `/agents/${agent.id}`) return json(route, agent)
  if (path === `/agents/${platformAgent.id}`) return json(route, platformAgent)
  if (path === `/agents/${agent.id}/status` || path === `/agents/${platformAgent.id}/status`) return json(route, { agentId: path.split('/')[2], busy: false, currentRun: null, sleep: null, sleepRevision: 0, queued: { interrupt: 0, normal: 0 }, processState: 'idle', processPid: null, processStartedAt: null, session: agent.session })
  if (path === `/agents/${agent.id}/skills` || path === `/agents/${platformAgent.id}/skills` || path === '/skills') return json(route, { skills: [] })
  if (path === `/agents/${platformAgent.id}/model-auth`) return json(route, { credential: null, operation: null })
  if (path === '/model-providers') return json(route, providerList)
  if (/^\/model-providers\/[^/]+\/models$/.test(path)) return json(route, models(path.split('/')[2]))
  if (path === '/cli-templates') return json(route, { templates })
  if (path === `/projects/${project.id}/credential-accounts`) return json(route, { projectId: project.id, credentialsRevision: 1, accounts })
  if (path === `/projects/${project.id}/credential-account-impact`) return json(route, { projectId: project.id, credentialsRevision: 1, accountId: url.searchParams.get('accountId'), agents: [{ agentId: agent.id, name: agent.name, mode: 'automatic', selectedAccountId: null, resolvedAccountId: modelAccount.id, status: 'resolved' }] })
  if (path === `/projects/${project.id}/model-auth/operations`) {
    if (method === 'POST') return json(route, { operation: { id: id('10'), projectId: project.id, provider: 'openai-codex', targetAccountId: codexAccount.id, targetRevision: 1, name: null, status: 'failed', accountId: null, startedAt: now, expiresAt: now, completedAt: now, authorizationUrl: null, errorCode: 'auth_port_busy', errorMessage: null }, account: codexAccount }, 202)
    return json(route, { operations: [] })
  }
  if (path === `/projects/${project.id}` && method === 'DELETE') return json(route, { error: { code: 'task_not_idle', message: '仍有未结束任务', details: { projectId: project.id, tasks: [{ id: id('11'), status: 'running' }] } } }, 409)
  if (path === `/projects/${project.id}/credential-accounts/${modelAccount.id}`) return json(route, accounts[0])
  if (path === `/projects/${project.id}/credential-accounts/${modelAccount.id}/credential` && method === 'PUT') {
    assert.equal(request.postDataJSON().expectedRevision, 1)
    accounts = [{ ...modelAccount, revision: 2, name: '另一个页面更新后的名称' }, ...accounts.slice(1)]; conflict = true
    return json(route, { error: { code: 'precondition_failed', message: '账号已被修改', details: { resourceKind: 'account', resourceId: modelAccount.id, expectedVersion: 1, currentVersion: 2 } } }, 412)
  }
  if (path === `/agents/${agent.id}/credential-selections`) {
    if (method === 'PUT') {
      const body = request.postDataJSON(); assert.equal(body.provider, 'anthropic'); assert.equal(body.accountId, null)
      agent.configVersion = 2
      selection = { ...selection, configVersion: 2, selections: [{ ...selection.selections[0], mode: 'automatic', status: 'resolved', resolvedAccountId: modelAccount.id, resolvedVersionId: modelAccount.currentVersionId }, selection.selections[1]] }
    }
    return json(route, selection)
  }
  unhandled.push({ method, path }); return json(route, { error: { code: 'not_found', message: `fixture未覆盖 ${method} ${path}` } }, 404)
})
const ok = message => { checks.push(message); console.log('PASS ' + message) }
const overflow = async () => assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth), false)
const shot = async (locator, name) => { await locator.screenshot({ path: new URL(name + '.png', out).pathname }); await overflow() }
try {
  await page.goto(origin + base + '/settings')
  const model = page.getByRole('region', { name: '模型账号', exact: true }), cli = page.getByRole('region', { name: 'CLI 账号', exact: true })
  await model.getByText(modelAccount.name, { exact: true }).waitFor()
  await shot(model, 'model-desktop'); await shot(cli, 'cli-desktop')
  await model.getByRole('button', { name: '整体替换凭证' }).click()
  await model.getByLabel('API Key', { exact: true }).fill('fixture-only-secret')
  await model.getByRole('button', { name: '保存', exact: true }).click()
  await model.getByText('配置已在别处更新，尚未保存本次修改').waitFor()
  assert.ok(conflict); assert.equal(await model.getByLabel('API Key', { exact: true }).inputValue(), 'fixture-only-secret')
  await shot(model.getByRole('alert'), 'conflict-desktop'); ok('实际React项目账号/CLI安全状态及CAS412遮蔽草稿，固定基线')
  await page.setViewportSize({ width: 390, height: 844 })
  await shot(model, 'conflict-mobile'); await shot(cli, 'cli-mobile'); ok('390实际页面无整页横向溢出，区块截图可扩高')
  await model.getByRole('button', { name: '取消', exact: true }).click()
  await page.goto(origin + base + `/agents/${agent.id}/config`)
  const accountArea = page.getByRole('region', { name: '模型账号凭证', exact: true })
  await accountArea.getByText(/原模型设置已清除/).waitFor()
  assert.equal(await page.getByRole('button', { name: '录入 API Key' }).count(), 0)
  assert.equal(await page.getByRole('button', { name: '录入凭证' }).count(), 0)
  await shot(accountArea, 'legacy-mobile')
  await accountArea.getByRole('button', { name: '恢复当前提供方自动取用' }).click()
  await accountArea.getByText('自动使用唯一对应的项目账号').waitFor()
  assert.equal(writes.filter(write => write.path.endsWith('/credential-selections')).length, 1)
  await shot(accountArea, 'automatic-mobile'); ok('项目Agent无私有秘密表单，legacy按mode展示，恢复只发单provider PUT')
  await page.setViewportSize({ width: 1440, height: 1000 }); await shot(accountArea, 'automatic-desktop')
  await page.setViewportSize({ width: 390, height: 844 })
  for (const scene of ['zero', 'many', 'deleted']) {
    selection = { ...selection, configVersion: selection.configVersion + 1, selections: [{ ...selection.selections[0], mode: scene === 'deleted' ? 'explicit' : 'automatic', selectedAccountId: scene === 'deleted' ? id('99') : null, resolvedAccountId: null, resolvedVersionId: null, status: scene === 'zero' ? 'not_configured' : scene === 'many' ? 'selection_required' : 'account_deleted', candidates: scene === 'zero' ? [] : scene === 'many' ? [modelAccount, { ...modelAccount, id: id('12'), name: '备用失效账号', status: 'unavailable' }] : [modelAccount] }, selection.selections[1]] }
    agent.configVersion = selection.configVersion
    await page.reload(); await accountArea.getByRole('combobox').waitFor()
    if (scene === 'many') assert.equal(await accountArea.getByRole('combobox').inputValue(), '')
    if (scene === 'deleted') assert.equal(await accountArea.getByRole('combobox').inputValue(), id('99'))
    await shot(accountArea, `${scene}-mobile`)
  }
  ok('实际React零/多含不可用/明确已删状态，无默认第一条或回落')
  const clis = page.getByRole('region', { name: '可用 CLI', exact: true })
  await clis.getByRole('listitem', { name: 'gh' }).getByRole('button', { name: '移除' }).click()
  await shot(page.getByRole('alertdialog'), 'remove-cli-mobile'); await page.getByRole('alertdialog').getByRole('button', { name: '取消', exact: true }).click()
  await page.getByRole('region', { name: '删除 Agent', exact: true }).getByRole('button', { name: '删除这个 Agent' }).click()
  await shot(page.getByRole('alertdialog'), 'delete-agent-mobile'); await page.getByRole('alertdialog').getByRole('button', { name: '取消', exact: true }).click()
  await page.goto(origin + base + '/settings')
  await model.getByText(codexAccount.name, { exact: true }).waitFor()
  await model.getByRole('button', { name: '重新登录', exact: true }).click()
  await model.getByText('本次登录失败', { exact: true }).waitFor()
  await shot(model.locator('li').filter({ hasText: codexAccount.name }), 'login-failed-mobile')
  await page.getByRole('region', { name: '删除项目', exact: true }).getByRole('button', { name: '删除这个项目' }).click()
  await shot(page.getByRole('alertdialog'), 'delete-project-mobile')
  await page.getByRole('alertdialog').getByRole('button', { name: '删除项目', exact: true }).click()
  await page.getByText('项目仍有未结束任务，项目和账号没有删除。sleep 任务也须结束。').waitFor()
  await shot(page.getByRole('region', { name: '删除项目', exact: true }), 'delete-blocked-mobile')
  ok('三类删除说明/任务结构化链接及登录失败保旧，HTTP fixture不触发真实删除或授权')
  await page.goto(origin + `/platform/agents/${platformAgent.id}/config`)
  const privateArea = page.getByRole('region', { name: '模型账号凭证', exact: true })
  await privateArea.getByRole('button', { name: '录入 API Key', exact: true }).waitFor()
  await shot(privateArea, 'platform-private-mobile')
  await privateArea.getByRole('button', { name: '录入 API Key', exact: true }).focus()
  await page.keyboard.press('Enter')
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('API Key', { exact: true }).waitFor()
  assert.equal(await dialog.getByLabel('API Key', { exact: true }).getAttribute('type'), 'password')
  await shot(dialog, 'platform-keyboard-mobile'); ok('平台私有表单保留，键盘打开及遮蔽字段')
  assert.deepEqual(errors, []); assert.deepEqual(blocked, []); assert.deepEqual(unhandled, [])
  ok('浏览器0异常，所有API为HTTP fixture，0供应商或DEV网络请求')
  writeFileSync(new URL('results.json', out), JSON.stringify({ level: 'React + intercepted HTTP fixture; no App/PG/OAuth', origin, viewports: ['1440x1000', '390x844'], screenshotBoundary: 'region screenshots may exceed viewport height', checks, errors, blocked, unhandled, httpConsole, writes }, null, 2))
} catch (error) {
  writeFileSync(new URL('failure.json', out), JSON.stringify({ message: error.message, checks, errors, blocked, unhandled }, null, 2)); throw error
} finally { await browser.close() }
