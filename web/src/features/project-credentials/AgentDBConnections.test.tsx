import { act, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject, errorBody, makeAgent } from '@/test/api'
import { emptySelections, projectAccount } from '@/test/project-credentials'
import { renderConfig } from '@/test/config'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { ScopeContext } from '@/features/projects/scope-context'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { dbConnectionKey } from '@/api/db-connections'
import { AgentDBConnections } from './AgentDBConnections'
import { CredentialFields } from './CredentialFields'
import { AccountImpact } from './ProjectAccounts'
import { CliToolStatus } from '@/features/agent-config/CredentialStatus'

const agent = makeAgent()
const a = projectAccount({ purpose: 'cli', provider: null, cli: 'psql', name: '连接A', authMethod: null })
const b = projectAccount({ ...a, id: '10000000-0000-7000-8000-000000000002', name: '连接B' })
function connections(overrides: Partial<Schemas['DBConnections']> = {}): Schemas['DBConnections'] {
  return { agentId: agent.id, projectId: defaultProject.id, cli: 'psql', configVersion: 1, credentialsRevision: 1, mode: 'explicit_set', accountIds: [], connections: [], status: 'not_configured', ...overrides }
}
function renderDB(read: () => Schemas['DBConnections'], accounts = [a, b]) {
  server.use(
    http.get(apiUrl('/agents/:agentId/db-connections/:cli'), () => HttpResponse.json(read())),
    http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts })),
  )
  return renderRoutes([{ path: '/', element: <ScopeContext value={{ kind: 'project', project: defaultProject }}><DraftNavigationGuard><AgentDBConnections agentId={agent.id} configVersion={1} cli="psql" /></DraftNavigationGuard></ScopeContext> }], '/')
}

describe('项目Agent DB集合正式HTTP fixture', () => {
  it('干净表单跟随同configVersion下解析集合变化，不保留旧自动账号', async () => {
    let data = connections({ accountIds: [a.id], status: 'resolved', connections: [{ accountId: a.id, name: a.name, versionId: a.currentVersionId, status: 'resolved' }] })
    const env = renderDB(() => data)
    await waitFor(() => expect(screen.getAllByRole('checkbox')).toHaveLength(2))
    expect(screen.getAllByRole('checkbox')[0]).toBeChecked()
    data = connections({ credentialsRevision: 2 })
    await act(() => env.client.invalidateQueries({ queryKey: dbConnectionKey(agent.id, 'psql') }))
    await waitFor(() => screen.getAllByRole('checkbox').forEach(box => expect(box).not.toBeChecked()))
  })

  it('无集合沿旧single编辑，切显式空集合后移除旧写入口', async () => {
    const user = userEvent.setup()
    let data = connections({ mode: 'legacy_single' })
    const selection = emptySelections({ ...agent, clis: [{ name: 'psql', hasTemplate: true, credential: null }] })
    server.use(http.get(apiUrl('/agents/:agentId/credential-selections'), () => HttpResponse.json(selection)),
      http.put(apiUrl('/agents/:agentId/db-connections/:cli'), () => { data = connections(); return HttpResponse.json(data) }))
    renderDB(() => data, [])
    expect(await screen.findByLabelText('psql 项目账号')).toBeInTheDocument()
    await user.click(await screen.findByRole('button', { name: '保存空集合（不授权连接）' }))
    await waitFor(() => expect(screen.queryByLabelText('psql 项目账号')).toBeNull())
    expect(await screen.findByText('当前模式：明确连接集合（旧单账号编辑已停用） · 未配置连接')).toBeInTheDocument()
  })

  it('账号库两候选不自动授权，按稳定ID一次全量PUT，不写旧single', async () => {
    const user = userEvent.setup(), writes: unknown[] = []
    let data = connections(), singles = 0
    server.use(http.put(apiUrl('/agents/:agentId/db-connections/:cli'), async ({ request }) => {
      writes.push(await request.json()); data = connections({ accountIds: [a.id, b.id], status: 'resolved', connections: [a, b].map(item => ({ accountId: item.id, name: item.name, versionId: item.currentVersionId, status: 'resolved' })) }); return HttpResponse.json(data)
    }), http.put(apiUrl('/agents/:agentId/credential-selections'), () => { singles++; return HttpResponse.json({}) }))
    renderDB(() => data)
    const boxes = await screen.findAllByRole('checkbox')
    expect(boxes).toHaveLength(2); boxes.forEach(box => expect(box).not.toBeChecked())
    expect(screen.queryByRole('combobox')).toBeNull()
    await user.click(boxes[1]); await user.click(boxes[0]); await user.click(screen.getByRole('button', { name: '保存连接集合' }))
    await waitFor(() => expect(writes).toEqual([{ accountIds: [a.id, b.id], expectedConfigVersion: 1 }]))
    expect(singles).toBe(0)
  })

  it('显式清空PUT[]与DELETE恢复旧模式有不同请求/不删除账号', async () => {
    const user = userEvent.setup(), writes: unknown[] = [], deletes: string[] = []
    let data = connections({ accountIds: [a.id], connections: [{ accountId: a.id, name: a.name, versionId: a.currentVersionId, status: 'resolved' }], status: 'resolved' })
    server.use(http.put(apiUrl('/agents/:agentId/db-connections/:cli'), async ({ request }) => { writes.push(await request.json()); data = connections(); return HttpResponse.json(data) }),
      http.delete(apiUrl('/agents/:agentId/db-connections/:cli'), ({ request }) => { deletes.push(new URL(request.url).search); return HttpResponse.json(data) }))
    renderDB(() => data)
    await user.click((await screen.findAllByRole('checkbox'))[0])
    await user.click(screen.getByRole('button', { name: '保存空集合（不授权连接）' }))
    await waitFor(() => expect(writes).toEqual([{ accountIds: [], expectedConfigVersion: 1 }]))
    await waitFor(() => expect(screen.getByRole('button', { name: '删除集合配置，恢复旧模式' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '删除集合配置，恢复旧模式' }))
    await waitFor(() => expect(deletes).toEqual(['?expectedConfigVersion=1']))
    expect(writes).toHaveLength(1)
  })

  it('412保留集合草稿且仅发一次；取最新失败不能采用，核对后用固定新版本', async () => {
    const user = userEvent.setup(), writes: unknown[] = []
    let readFail = false, newest = false
    server.use(http.put(apiUrl('/agents/:agentId/db-connections/:cli'), async ({ request }) => { writes.push(await request.json()); readFail = true; return HttpResponse.json(errorBody('precondition_failed'), { status: 412 }) }))
    const env = renderDB(() => connections())
    server.use(http.get(apiUrl('/agents/:agentId/db-connections/:cli'), () => readFail ? HttpResponse.json(errorBody('internal_error'), { status: 500 }) : HttpResponse.json(connections({ configVersion: newest ? 2 : 1 }))))
    await user.click((await screen.findAllByRole('checkbox'))[1]); await user.click(screen.getByRole('button', { name: '保存连接集合' }))
    await screen.findByText('配置已在别处更新，尚未保存本次修改')
    expect((screen.getAllByRole('checkbox'))[1]).toBeChecked()
    expect(screen.getByRole('button', { name: '已核对，采用此版本' })).toBeDisabled()
    expect(writes).toHaveLength(1)
    readFail = false; newest = true
    await user.click(screen.getByRole('button', { name: '重新读取最新信息' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '已核对，采用此版本' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: '已核对，采用此版本' }))
    await user.click(screen.getByRole('button', { name: '保存连接集合' }))
    await waitFor(() => expect(writes).toEqual([{ accountIds: [b.id], expectedConfigVersion: 1 }, { accountIds: [b.id], expectedConfigVersion: 2 }]))
    expect(JSON.stringify(env.client.getMutationCache().getAll())).not.toContain('accountIds')
  })

  it('远端更新不覆盖dirty集合，删除成员仍保留原ID/逐项失效，不补选候选B', async () => {
    const user = userEvent.setup()
    let data = connections({ accountIds: [a.id], status: 'account_deleted', connections: [{ accountId: a.id, name: a.name, versionId: null, status: 'account_deleted' }] })
    const env = renderDB(() => data, [b])
    expect(await screen.findByText('账号已删除')).toBeInTheDocument()
    await waitFor(() => expect(screen.getAllByRole('checkbox')).toHaveLength(2))
    const boxes = screen.getAllByRole('checkbox'); expect(boxes[0]).toBeChecked(); expect(boxes[1]).not.toBeChecked()
    await user.click(boxes[1])
    data = connections({ configVersion: 2, accountIds: [] })
    await act(() => env.client.invalidateQueries({ queryKey: dbConnectionKey(agent.id, 'psql') }))
    expect(screen.getAllByRole('checkbox')).toHaveLength(2)
    screen.getAllByRole('checkbox').forEach(box => expect(box).toBeChecked())
    await waitFor(() => expect(screen.getByRole('button', { name: '保存连接集合' })).toBeDisabled())
  })
})

it('平台psql保留私有单槽，不查询项目DB集合', async () => {
  let dbReads = 0
  server.use(http.get(apiUrl('/agents/:agentId/db-connections/:cli'), () => { dbReads++; return HttpResponse.json(connections()) }))
  await renderConfig({ agent: makeAgent({ scope: 'platform', projectId: null, clis: [{ name: 'psql', hasTemplate: true, credential: null }] }), templates: [{ name: 'psql', version: '18.3', fields: [{ name: 'password', label: '密码', type: 'secret', required: true }], injection: 'files', install: { status: 'not_installed' }, warnings: [] }] })
  expect(await screen.findByRole('button', { name: '录入凭证' })).toBeInTheDocument()
  expect(screen.queryByText('psql 授权连接集合')).toBeNull()
  expect(dbReads).toBe(0)
})

it('正式impact集合成员明确标识连接集合，不冒充自动取用', async () => {
  server.use(http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, accountId: a.id, credentialsRevision: 2, agents: [{ agentId: agent.id, name: '数据库助手', mode: 'connection_set', configVersion: 1 }] })))
  renderRoutes([{ path: '/', element: <AccountImpact projectId={defaultProject.id} purpose="cli" slot="psql" accountId={a.id} /> }], '/')
  expect(await screen.findByText(/数据库助手（连接集合）/)).toBeInTheDocument()
  expect(screen.queryByText(/数据库助手（自动取用）/)).toBeNull()
  expect(screen.getByText(/删除后明确选择或连接集合中的该项将失效/)).toBeInTheDocument()
})

it('工具未就绪按正式安装原因展示，不把账号保存当作工具或连接成功', () => {
  renderRoutes([{ path: '/', element: <CliToolStatus template={{ name: 'psql', version: '18.3', fields: [], injection: 'files', install: { status: 'failed', error: '必要运行库缺失' }, warnings: [] }} /> }], '/')
  expect(screen.getByText('工具准备：安装失败 · psql v18.3')).toBeInTheDocument()
  expect(screen.getByText('必要运行库缺失')).toBeInTheDocument()
  expect(screen.getByText('工具状态与账号录入、远端连接或认证结果分开。')).toBeInTheDocument()
})

it('正式multiline保留PEM换行，私钥输入遮蔽且只绘制掩码', async () => {
  const user = userEvent.setup()
  function Fields() {
    const [values, setValues] = useState<Record<string, string>>({})
    return <CredentialFields fields={[{ name: 'ca', label: 'CA证书', type: 'string', required: false, multiline: true }, { name: 'key', label: '客户端私钥', type: 'secret', required: false, multiline: true }]} values={values} onChange={(name, value) => setValues(previous => ({ ...previous, [name]: value }))} />
  }
  renderRoutes([{ path: '/', element: <Fields /> }], '/')
  await user.type(screen.getByLabelText('CA证书'), 'CERT{enter}LINE')
  await user.type(screen.getByLabelText('客户端私钥'), 'PRIVATE{enter}KEY')
  expect(screen.getByLabelText('CA证书')).toHaveValue('CERT\nLINE')
  expect(screen.getByLabelText('客户端私钥')).toHaveValue('PRIVATE\nKEY')
  expect(screen.getByLabelText('客户端私钥')).toHaveClass('text-transparent', 'selection:text-transparent')
  expect(screen.queryByText('PRIVATE')).toBeNull()
})
