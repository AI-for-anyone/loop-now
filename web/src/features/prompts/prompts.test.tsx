import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { keys } from '@/api/queries'
import { projectPromptKey } from '@/api/prompts'
import { routes } from '@/app/routes'
import { apiUrl, defaultProject, errorBody, makeAgent } from '@/test/api'
import { sidebar } from '@/test/shell'
import { renderConfig } from '@/test/config'
import { projectBackend } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'

const promptInput = () => screen.getByLabelText('Agent Prompt')
const saveAgent = () => screen.getByRole('button', { name: '保存 Agent Prompt' })
const basic = () => screen.getByRole('region', { name: '基本信息' })
function setupProject(handlers: HttpHandler[] = []) {
  const backend = projectBackend()
  server.use(...handlers, ...backend.handlers)
  return { ...backend, ...renderRoutes(routes, `/p/${encodeURIComponent(defaultProject.name)}/settings`), user: userEvent.setup() }
}

describe('Agent Prompt正式CAS与独立草稿', () => {
  it('同文本也提交旧基线并受412保护；核对V2后后台V3不暗改采纳版本', async () => {
    const agent = makeAgent({ prompt: '原文' }), submissions: Schemas['AgentUpdate'][] = []
    let current = agent
    const { user, client } = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
      http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => { submissions.push(await request.json() as Schemas['AgentUpdate']); return HttpResponse.json(errorBody('precondition_failed'), { status: 412 }) }),
    ] })
    await user.clear(promptInput()); await user.type(promptInput(), '草稿')
    current = { ...agent, prompt: '草稿', promptVersion: 2 }
    await act(() => client.invalidateQueries({ queryKey: keys.agent(agent.id) }))
    await user.click(saveAgent()); await screen.findByText('服务器文本 · 版本 2')
    expect(submissions[0]).toEqual({ prompt: '草稿', expectedPromptVersion: 1 })
    current = { ...current, prompt: '服务器第三版', promptVersion: 3 }
    await act(() => client.invalidateQueries({ queryKey: keys.agent(agent.id) }))
    expect(screen.getByText('服务器文本 · 版本 2')).toBeInTheDocument()
    expect(screen.queryByText('服务器文本 · 版本 3')).toBeNull()
    await user.click(screen.getByRole('button', { name: '已核对版本 2，保留草稿继续编辑' }))
    expect(promptInput()).toHaveValue('草稿'); expect(screen.getByText('编辑基线：版本 2')).toBeInTheDocument()
    await user.click(saveAgent()); await screen.findByText('服务器文本 · 版本 3')
    expect(submissions[1]).toEqual({ prompt: '草稿', expectedPromptVersion: 2 })
  })
  it('412后GET失败保留草稿和原基线，不允许采纳；显式重读后可核对', async () => {
    const agent = makeAgent({ prompt: '原文' }); let failRead = false
    const { user } = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => failRead ? HttpResponse.json(errorBody('internal_error'), { status: 500 }) : HttpResponse.json(agent)),
      http.patch(apiUrl(`/agents/${agent.id}`), () => { failRead = true; return HttpResponse.json(errorBody('precondition_failed'), { status: 412 }) }),
    ] })
    await user.type(promptInput(), '草稿'); await user.click(saveAgent())
    await screen.findByText(/无法读取最新内容。草稿与原编辑基线已保留/)
    expect(promptInput()).toHaveValue('原文草稿'); expect(screen.getByText('编辑基线：版本 1')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /已核对版本/ })).toBeNull()
    failRead = false; await user.click(screen.getByRole('button', { name: '重新读取最新内容' })); await screen.findByText('服务器文本 · 版本 1')
  })
  it.each(['basic', 'prompt'] as const)('保存%s后，另一块仍dirty且只有一个离开确认', async savedBlock => {
    const agent = makeAgent(); let current = agent; const submissions: unknown[] = []
    const { user, router } = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
      http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => { const body = await request.json() as Schemas['AgentUpdate']; submissions.push(body); current = { ...current, ...body, promptVersion: current.promptVersion + ('prompt' in body ? 1 : 0) }; return HttpResponse.json(current) }),
    ] })
    await user.type(screen.getByLabelText('显示名称'), '改名'); await user.type(promptInput(), '草稿')
    await user.click(savedBlock === 'basic' ? within(basic()).getByRole('button', { name: '保存' }) : saveAgent())
    await waitFor(() => expect(savedBlock === 'basic' ? within(basic()).getByRole('button', { name: '保存' }) : saveAgent()).toBeDisabled())
    expect(savedBlock === 'basic' ? saveAgent() : within(basic()).getByRole('button', { name: '保存' })).toBeEnabled()
    expect(submissions[0]).toEqual(savedBlock === 'basic' ? { name: '开发助手改名' } : { prompt: '草稿', expectedPromptVersion: 1 })
    await user.click(screen.getByRole('link', { name: '去项目设置修改' }))
    expect(await screen.findAllByRole('alertdialog')).toHaveLength(1)
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '取消' }))
    expect(router.state.location.pathname).toMatch(/\/config$/); expect(promptInput()).toHaveValue('草稿')
  })
  it('项目规则刷新只更新继承区，不污染Agent草稿', async () => {
    let project: Schemas['ProjectPrompt'] = { prompt: '公共旧版', promptVersion: 1 }
    const { user, client } = await renderConfig({ handlers: [http.get(apiUrl('/projects/:projectId/prompt'), () => HttpResponse.json(project))] })
    await user.type(promptInput(), '我的职责'); project = { prompt: '公共新版', promptVersion: 2 }
    await act(() => client.invalidateQueries({ queryKey: projectPromptKey(defaultProject.id) }))
    expect(await screen.findByText('公共新版')).toBeInTheDocument(); expect(promptInput()).toHaveValue('我的职责')
    expect(screen.getByText('编辑基线：版本 1')).toBeInTheDocument()
  })
  it('平台Agent只编辑自身规则，不读取项目Prompt', async () => {
    const agent = makeAgent({ scope: 'platform', projectId: null }); let inheritedReads = 0
    const backend = projectBackend({ agents: [agent] })
    server.use(http.get(apiUrl('/projects/:projectId/prompt'), () => { inheritedReads++; return HttpResponse.json({ prompt: '不应读取', promptVersion: 1 }) }),
      http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: null, operation: null })), ...backend.handlers)
    renderRoutes(routes, `/platform/agents/${agent.id}/config`)
    await screen.findByLabelText('Agent Prompt')
    expect(screen.queryByRole('region', { name: '项目 Prompt（只读）' })).toBeNull()
    expect(screen.getByText('平台 Agent 只使用自己的 Prompt，不继承项目规则。')).toBeInTheDocument()
    expect(inheritedReads).toBe(0)
    expect(screen.getByText('当前回复继续使用原配置；下一条派发的消息使用新的 Agent Prompt。')).toBeInTheDocument()
    expect(within(screen.getByRole('region', { name: 'Prompt' })).queryByText(/两级快照/)).toBeNull()
  })
})

describe('项目Prompt独立保存与离开', () => {
  it('保存和清空携带自己的版本；离开有草稿会确认', async () => {
    let value: Schemas['ProjectPrompt'] = { prompt: '公共旧版', promptVersion: 1 }; const requests: unknown[] = []
    const { user, router } = setupProject([
      http.get(apiUrl('/projects/:projectId/prompt'), () => HttpResponse.json(value)),
      http.put(apiUrl('/projects/:projectId/prompt'), async ({ request }) => { const body = await request.json() as Schemas['ProjectPromptUpdate']; requests.push(body); value = { prompt: body.prompt, promptVersion: value.promptVersion + 1 }; return HttpResponse.json(value) }),
    ])
    const input = await screen.findByRole('textbox', { name: '项目 Prompt' }); await user.clear(input); await user.type(input, '公共新版')
    await user.click(screen.getByRole('button', { name: '保存项目 Prompt' })); await waitFor(() => expect(screen.getByRole('button', { name: '保存项目 Prompt' })).toBeDisabled())
    expect(requests[0]).toEqual({ prompt: '公共新版', expectedPromptVersion: 1 })
    await user.clear(input); await user.click(screen.getByRole('button', { name: '保存项目 Prompt' })); await waitFor(() => expect(requests).toHaveLength(2))
    expect(requests[1]).toEqual({ prompt: '', expectedPromptVersion: 2 })
    await waitFor(() => expect(screen.getByRole('button', { name: '保存项目 Prompt' })).toBeDisabled())
    await user.type(input, '未保存'); await user.click(within(sidebar()).getByRole('link', { name: '任务' }))
    await screen.findByRole('alertdialog'); expect(router.state.location.pathname).toMatch(/settings$/)
  })
  it('同projectId改名规范化URL保留Prompt草稿，不误弹离开确认', async () => {
    const { user, state, client, router } = setupProject()
    const input = await screen.findByRole('textbox', { name: '项目 Prompt' }); await user.type(input, '改名时的草稿')
    state.projects = state.projects.map(project => ({ ...project, name: '改名项目' }))
    await act(() => client.invalidateQueries({ queryKey: keys.projects }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/p/'+encodeURIComponent('改名项目')+'/settings'))
    expect(input).toHaveValue('改名时的草稿'); expect(screen.queryByRole('alertdialog')).toBeNull()
  })
  it('项目PUT冲突保留草稿，GET最新后显式核对再保存新基线', async () => {
    let value: Schemas['ProjectPrompt'] = { prompt: '原项目', promptVersion: 1 }; const requests: Schemas['ProjectPromptUpdate'][] = []
    const { user } = setupProject([
      http.get(apiUrl('/projects/:projectId/prompt'), () => HttpResponse.json(value)),
      http.put(apiUrl('/projects/:projectId/prompt'), async ({ request }) => {
        const body=await request.json() as Schemas['ProjectPromptUpdate'];requests.push(body)
        if(body.expectedPromptVersion!==value.promptVersion)return HttpResponse.json(errorBody('precondition_failed'),{status:412})
        value={prompt:body.prompt,promptVersion:value.promptVersion+1};return HttpResponse.json(value)
      }),
    ])
    const input=await screen.findByRole('textbox',{name:'项目 Prompt'});await user.clear(input);await user.type(input,'项目草稿')
    value={prompt:'其他页面的项目规则',promptVersion:2}
    await user.click(screen.getByRole('button',{name:'保存项目 Prompt'}));await screen.findByText('服务器文本 · 版本 2')
    expect(input).toHaveValue('项目草稿');expect(requests[0]).toEqual({prompt:'项目草稿',expectedPromptVersion:1})
    await user.click(screen.getByRole('button',{name:'已核对版本 2，保留草稿继续编辑'}));await user.click(screen.getByRole('button',{name:'保存项目 Prompt'}))
    await waitFor(()=>expect(screen.getByRole('button',{name:'保存项目 Prompt'})).toBeDisabled())
    expect(requests[1]).toEqual({prompt:'项目草稿',expectedPromptVersion:2})
  })

})
