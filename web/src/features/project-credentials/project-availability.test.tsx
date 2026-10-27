import userEvent from '@testing-library/user-event'
import { act, screen, waitFor } from '@testing-library/react'
import { focusManager } from '@tanstack/react-query'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, defaultProject, errorBody } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { providers } from '@/test/config'
import { credentialKeys } from '@/api/project-credentials'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { ProjectCredentialSettings } from './ProjectAccounts'

function renderSettings() {
  return renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectCredentialSettings projectId={defaultProject.id} /></DraftNavigationGuard> }], '/')
}

describe('旧后台项目凭证兼容', () => {
  it('精确缺失路由合并提示且不查询impact/operation，focus重读新后台自动恢复', async () => {
    let supported = false, lists = 0, ancillary = 0, writes = 0
    server.use(
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => {
        lists++
        return supported ? HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 0, accounts: [] }) : HttpResponse.json(errorBody('not_found', '接口不存在'), { status: 404 })
      }),
      http.get(apiUrl('/model-providers'), () => HttpResponse.json(providers)),
      http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
      http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => { ancillary++; return HttpResponse.json({}) }),
      http.get(apiUrl('/projects/:projectId/model-auth/operations'), () => { ancillary++; return HttpResponse.json({}) }),
      http.post(apiUrl('/projects/:projectId/credential-accounts'), () => { writes++; return HttpResponse.json({}) }),
    )
    renderSettings()
    await screen.findByText(/当前后台尚未支持项目凭证/)
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    expect(screen.getByRole('button', { name: '登录 Codex' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '录入账号' })).toBeDisabled()
    expect(screen.queryByText('尚未配置账号')).toBeNull()
    expect(screen.queryByLabelText('添加模型账号 · 提供方')).toBeNull()
    expect(ancillary).toBe(0); expect(writes).toBe(0)
    supported = true
    await act(async () => { focusManager.setFocused(false); focusManager.setFocused(true) })
    await screen.findByLabelText('添加模型账号 · 提供方')
    expect(screen.queryByText(/当前后台尚未支持项目凭证/)).toBeNull()
    expect(screen.getByLabelText('添加模型账号 · 提供方')).toBeEnabled()
    expect(screen.getAllByText('尚未配置账号')).toHaveLength(2)
    expect(lists).toBeGreaterThanOrEqual(2)
    expect(ancillary).toBe(0); expect(writes).toBe(0)
    focusManager.setFocused(undefined)
  })

  it('真实资源404仍是资源错误，不推断不支持或空账号，已有草稿暂隐藏后恢复', async () => {
    const user = userEvent.setup()
    let gone = false
    server.use(
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => gone ? HttpResponse.json(errorBody('not_found', '项目不存在'), { status: 404 }) : HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 0, accounts: [] })),
      http.get(apiUrl('/model-providers'), () => HttpResponse.json(providers)),
      http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
    )
    server.use(http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 0, accountId: null, agents: [] })))
    const { client } = renderSettings()
    await screen.findByLabelText('添加模型账号 · 提供方')
    await screen.findByRole('option', { name: 'anthropic' })
    await user.selectOptions(screen.getByLabelText('添加模型账号 · 提供方'), 'anthropic')
    await user.click(screen.getByRole('button', { name: '录入 API Key' }))
    await user.type(screen.getByLabelText('API Key'), 'local-fixture-draft')
    gone = true
    await act(() => client.invalidateQueries({ queryKey: credentialKeys.project(defaultProject.id) }))
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('资源不存在或已删除'))
    expect(screen.queryByText(/当前后台尚未支持项目凭证/)).toBeNull()
    expect(screen.queryByRole('combobox')).toBeNull()
    expect(screen.getByRole('button', { name: '登录 Codex' })).toBeDisabled()
    gone = false
    await act(() => client.invalidateQueries({ queryKey: credentialKeys.project(defaultProject.id) }))
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull())
    expect(screen.getByLabelText('API Key')).toBeVisible()
    expect(screen.getByLabelText('API Key')).toHaveValue('local-fixture-draft')
  })
})
