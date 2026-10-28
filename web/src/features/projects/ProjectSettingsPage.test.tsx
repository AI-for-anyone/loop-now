import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { defaultAgentsPath, defaultProject, makeAgent, makeProject } from '@/test/api'
import { chooseModel } from '@/test/model-picker'
import { projectBackend } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { findScopeSwitcher, sidebar } from '@/test/shell'

const base = (name: string) => `/p/${encodeURIComponent(name)}`

function setup(options: { agents?: 'some' | 'none' } = {}) {
  const other = makeProject({ name: '模型接入' })
  const dev = makeAgent({ name: '开发助手', projectId: other.id })
  const planner = makeAgent({ name: '规划助手', projectId: other.id })
  const backend = projectBackend({ projects: [defaultProject, other], agents: options.agents === 'none' ? [] : [dev, planner] })
  server.use(...backend.handlers)
  return { ...backend, other, dev, planner }
}

const card = (name: string) => screen.findByRole('region', { name })

describe('项目改名', () => {
  it('改名成功后地址换成新名称，范围切换显示新名称', async () => {
    const { state, other } = setup()
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, `${base('模型接入')}/settings`)
    const nameCard = await card('项目名称')
    expect(within(nameCard).getByText(other.id)).toBeInTheDocument()
    const input = within(nameCard).getByLabelText('项目名称')
    await user.clear(input)
    await user.type(input, '模型适配')
    await user.click(within(nameCard).getByRole('button', { name: '保存' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('模型适配')}/settings`))
    expect(state.requests).toContainEqual({ method: 'PATCH', path: `/api/v1/projects/${other.id}`, body: { name: '模型适配' } })
    expect(await findScopeSwitcher('模型适配')).toBeInTheDocument()
    const saved = await within(await card('项目名称')).findByRole('status')
    expect(saved).toHaveTextContent('已保存')
    expect(saved).toHaveTextContent('地址已改为 /p/模型适配/settings')
    expect(within(await card('项目名称')).getByLabelText('项目名称')).toHaveValue('模型适配')
    // 在本控制台改名，不贴“在别处改名”的便签。
    expect(screen.queryByText(/已在别处改名/)).toBeNull()
  })

  it('重名时在名称旁说明，项目不变', async () => {
    setup()
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, `${base('模型接入')}/settings`)
    const nameCard = await card('项目名称')
    const input = within(nameCard).getByLabelText('项目名称')
    await user.clear(input)
    await user.type(input, '测试项目')
    await user.click(within(nameCard).getByRole('button', { name: '保存' }))
    expect(await within(nameCard).findByText('已有同名的项目，请换一个名称')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe(`${base('模型接入')}/settings`)
  })

  it('默认项目可以改名，不能删除', async () => {
    setup()
    renderRoutes(routes, `${base('测试项目')}/settings`)
    expect(within(await card('项目名称')).getByRole('textbox', { name: '项目名称' })).toBeEnabled()
    const danger = await card('删除项目')
    expect(within(danger).getByText('默认项目不能删除，可以改名。')).toBeInTheDocument()
    expect(within(danger).queryByRole('button')).toBeNull()
  })
})

describe('项目 Manager', () => {
  it('指定、更换、取消 Manager；列表与详情标出 Manager', async () => {
    const { state, other, dev, planner } = setup()
    const user = userEvent.setup()
    renderRoutes(routes, `${base('模型接入')}/settings`)
    const managerCard = await card('Manager')
    expect(within(managerCard).getByText('该项目还没有 Manager')).toBeInTheDocument()

    await user.click(await within(managerCard).findByRole('combobox', { name: '指定 Manager' }))
    await user.click(await screen.findByRole('option', { name: '开发助手' }))
    expect(await within(managerCard).findByText('已指定「开发助手」为 Manager')).toBeInTheDocument()
    expect(within(managerCard).getByRole('link', { name: '开发助手' })).toHaveAttribute('href', `${base('模型接入')}/agents/${dev.id}/chat`)
    expect(state.requests).toContainEqual({ method: 'PUT', path: `/api/v1/projects/${other.id}/manager`, body: { agentId: dev.id } })

    await user.click(within(managerCard).getByRole('combobox', { name: '更换 Manager' }))
    await user.click(await screen.findByRole('option', { name: '规划助手' }))
    expect(await within(managerCard).findByText('已指定「规划助手」为 Manager')).toBeInTheDocument()
    expect(state.projects.find((p) => p.id === other.id)?.managerAgentId).toBe(planner.id)

    await user.click(within(managerCard).getByRole('button', { name: '取消 Manager' }))
    expect(await within(managerCard).findByText('已取消 Manager')).toBeInTheDocument()
    expect(within(managerCard).getByText('该项目还没有 Manager')).toBeInTheDocument()
    expect(state.requests.at(-1)).toMatchObject({ method: 'DELETE', path: `/api/v1/projects/${other.id}/manager` })
  })

  it('Agent 列表与详情头部标出 Manager', async () => {
    const { state, other, dev } = setup()
    state.projects.find((p) => p.id === other.id)!.managerAgentId = dev.id
    renderRoutes(routes, `${base('模型接入')}/agents/${dev.id}/config`)
    const list = await screen.findByRole('list', { name: 'Agent' })
    await waitFor(() => expect(within(list).getByRole('link', { name: /开发助手/ })).toHaveTextContent('Manager'))
    expect(within(list).getByRole('link', { name: /规划助手/ })).not.toHaveTextContent('Manager')
    const heading = await screen.findByRole('heading', { level: 1, name: '开发助手' })
    expect(heading.parentElement).toHaveTextContent('Manager')
  })

  it('项目中没有 Agent 时，说明需要先新建 Agent', async () => {
    setup({ agents: 'none' })
    renderRoutes(routes, `${base('模型接入')}/settings`)
    const managerCard = await card('Manager')
    expect(await within(managerCard).findByText('项目里还没有 Agent，需要先新建 Agent。')).toBeInTheDocument()
    await userEvent.setup().click(within(managerCard).getByRole('button', { name: '去新建 Agent' }))
    expect(await screen.findByRole('dialog', { name: '新建 Agent' })).toBeInTheDocument()
  })
})

describe('删除项目', () => {
  it('确认框说明 Agent 数；有 Agent 不空闲时列出它们；全部空闲后删除并进入默认项目', async () => {
    const { state, other, dev } = setup()
    state.busy.add(dev.id)
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, `${base('模型接入')}/settings`)
    const danger = await card('删除项目')
    await user.click(within(danger).getByRole('button', { name: '删除这个项目' }))
    const confirm = await screen.findByRole('alertdialog', { name: '删除项目「模型接入」？' })
    expect(confirm).toHaveTextContent('将同时删除项目中的 2 个 Agent、项目共享账号及凭证')
    await user.click(within(confirm).getByRole('button', { name: '删除项目' }))

    const alert = await within(danger).findByRole('alert')
    expect(alert).toHaveTextContent('项目中有 Agent 不空闲，项目没有删除。')
    expect(within(alert).getByRole('link', { name: '进入「开发助手」的对话' })).toHaveAttribute('href', `${base('模型接入')}/agents/${dev.id}/chat`)
    expect(router.state.location.pathname).toBe(`${base('模型接入')}/settings`)

    state.busy.clear()
    await user.click(within(danger).getByRole('button', { name: '删除这个项目' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: '删除项目' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(defaultAgentsPath))
    expect(state.projects.map((p) => p.id)).not.toContain(other.id)
    await user.click(await findScopeSwitcher('测试项目'))
    await waitFor(() =>
      expect(within(screen.getByRole('listbox', { name: '范围' })).queryByRole('option', { name: /模型接入/ })).toBeNull(),
    )
  })
})

describe('按作用域新建与删除 Agent', () => {
  it('在平台中新建 Agent：建在平台，出现在平台的列表中', async () => {
    const { state } = setup()
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, '/platform/agents')
    await user.click(await within(sidebar()).findByRole('button', { name: '新建 Agent' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('显示名称'), '巡检助手')
    await user.selectOptions(await within(dialog).findByLabelText('模型提供方'), 'anthropic')
    await chooseModel(user, dialog, 'claude-sonnet-5-5')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/platform\/agents\/.+\/config$/))
    expect(state.requests).toContainEqual(expect.objectContaining({ method: 'POST', path: '/api/v1/platform/agents' }))
    expect(state.agents.find((a) => a.name === '巡检助手')).toMatchObject({ scope: 'platform', projectId: null })
    await waitFor(() => expect(within(screen.getByRole('list', { name: '平台 Agent' })).getByRole('link', { name: /巡检助手/ })).toBeInTheDocument())
  })

  it('删除担任 Manager 的 Agent：确认框说明 Manager 会被取消，删除后进入所在项目剩下的 Agent', async () => {
    const { state, other, dev, planner } = setup()
    state.projects.find((p) => p.id === other.id)!.managerAgentId = dev.id
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, `${base('模型接入')}/agents/${dev.id}/config`)
    await user.click(await screen.findByRole('button', { name: '删除这个 Agent' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('它担任项目 Manager，该项目的 Manager 将同时被取消。')
    await user.click(within(confirm).getByRole('button', { name: '删除' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('模型接入')}/agents/${planner.id}/chat`))
    expect(state.projects.find((p) => p.id === other.id)?.managerAgentId).toBeNull()
  })
})
