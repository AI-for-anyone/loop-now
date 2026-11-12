import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { renderConfig, withClaudeCode } from '@/test/config'
import { chooseModel } from '@/test/model-picker'
import { runtimeList } from '@/test/runtimes'
import { thinkingOptions } from '@/test/thinking'

// 运行时由模型提供方决定：选本机的 Claude Code 就是 Claude Code 运行时，其余是 Pi。
const region = (name: string) => screen.getByRole('region', { name })
const saveButton = () => within(region('基本信息')).getByRole('button', { name: '保存' })
const option = (select: HTMLElement, value: string) => within(select).queryAllByRole('option').find((item) => (item as HTMLOptionElement).value === value) as HTMLOptionElement | undefined
const providerNote = '用本机安装的 Claude Code 和你在本机的登录，平台不保存登录信息。'
const differencesTitle = '用 Claude Code 运行时'
const claudeAgent = (overrides: Partial<Schemas['Agent']> = {}) => makeAgent({ model: { provider: 'claude-code', modelId: 'sonnet' }, runtime: 'claude-code', ...overrides })
const catalog = http.get(apiUrl('/model-providers'), () => HttpResponse.json(withClaudeCode))

describe('选择 Claude Code', () => {
  it('Pi 的 Agent 改选 Claude Code：注明用本机的登录、列出与 Pi 的不同，思考等级按目录、不显示速度档', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({ agent, handlers: [catalog] })
    const basic = region('基本信息')
    const select = within(basic).getByLabelText('模型提供方')
    await waitFor(() => expect(option(select, 'claude-code')).toBeDefined())
    expect(option(select, 'claude-code')).toHaveTextContent(/^Claude Code（本机，2 个模型）$/)
    expect(option(select, 'claude-code')).toBeEnabled()
    expect(within(basic).queryByText(providerNote)).toBeNull()
    expect(within(basic).queryByRole('region', { name: differencesTitle })).toBeNull()

    await user.selectOptions(select, 'claude-code')
    expect(within(basic).getByText(providerNote)).toHaveAttribute('role', 'note')
    const differences = within(basic).getByRole('region', { name: differencesTitle })
    expect(within(differences).getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      '用你本机的家目录和登录运行：它执行 gh、git 等命令时用的是你本人的登录。',
      '不经确认直接执行命令，与 Pi 的 Agent 相同。',
      '只含 Pi 扩展的插件对它不起作用；插件的技能和 MCP 工具可以用。',
      '没有 /pi: 命令。',
    ])
    await chooseModel(user, basic, 'opus')
    expect(thinkingOptions(basic)).toEqual(['默认（中）', '低', '中', '高', '更高', '最高'])
    expect(within(basic).queryByLabelText('Codex 速度')).toBeNull()

    // 换回 Pi 的提供方，说明随之收起。
    await user.selectOptions(select, 'anthropic')
    expect(within(basic).queryByText(providerNote)).toBeNull()
    expect(within(basic).queryByRole('region', { name: differencesTitle })).toBeNull()
  })

  it('保存时运行时要变：先确认，取消不保存；确认后保存', async () => {
    const agent = makeAgent()
    const bodies: unknown[] = []
    let current = agent
    const { user } = await renderConfig({
      agent,
      handlers: [
        catalog,
        http.get(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(current)),
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          bodies.push(await request.json())
          current = claudeAgent({ id: agent.id, configVersion: 2, session: { state: 'none', epoch: 2 } })
          return HttpResponse.json(current)
        }),
      ],
    })
    const basic = region('基本信息')
    const select = within(basic).getByLabelText('模型提供方')
    await waitFor(() => expect(option(select, 'claude-code')).toBeDefined())
    await user.selectOptions(select, 'claude-code')
    await chooseModel(user, basic, 'sonnet')

    await user.click(saveButton())
    const dialog = await screen.findByRole('alertdialog', { name: '切换运行时？' })
    expect(dialog).toHaveTextContent('运行时会从 Pi 换成 Claude Code。Agent 会从新会话开始：原会话保留，但不带到新会话里；原会话里等待中的 sleep 会取消。')
    await user.click(within(dialog).getByRole('button', { name: '取消' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(bodies).toEqual([])

    await user.click(saveButton())
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: '切换并保存' }))
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'sonnet' } }]))
    expect(await within(basic).findByText('已保存')).toBeInTheDocument()
  })

  it('Claude Code 换回 Pi 的提供方同样要确认；同一运行时里换模型不确认', async () => {
    const agent = claudeAgent()
    const bodies: unknown[] = []
    const { user } = await renderConfig({
      agent,
      handlers: [
        catalog,
        http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => {
          const body = (await request.json()) as Schemas['AgentUpdate']
          bodies.push(body)
          return HttpResponse.json({ ...agent, model: body.model ?? agent.model, configVersion: agent.configVersion + 1 })
        }),
      ],
    })
    const basic = region('基本信息')
    await chooseModel(user, basic, 'opus')
    await user.click(saveButton())
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'opus' } }]))
    expect(screen.queryByRole('alertdialog')).toBeNull()

    await user.selectOptions(within(basic).getByLabelText('模型提供方'), 'anthropic')
    await chooseModel(user, basic, 'claude-haiku-5-5')
    await user.click(saveButton())
    expect(await screen.findByRole('alertdialog', { name: '切换运行时？' })).toHaveTextContent('运行时会从 Claude Code 换成 Pi。')
  })

  it('Agent 正在回复时切换（409 agent_not_idle）：说明等这次回复结束后再切换', async () => {
    const agent = makeAgent()
    const { user } = await renderConfig({
      agent,
      handlers: [
        catalog,
        http.patch(apiUrl(`/agents/${agent.id}`), () => HttpResponse.json(errorBody('agent_not_idle', 'Agent 正在回复，请等这次回复结束后再切换运行时'), { status: 409 })),
      ],
    })
    const basic = region('基本信息')
    const select = within(basic).getByLabelText('模型提供方')
    await waitFor(() => expect(option(select, 'claude-code')).toBeDefined())
    await user.selectOptions(select, 'claude-code')
    await chooseModel(user, basic, 'sonnet')
    await user.click(saveButton())
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: '切换并保存' }))
    expect(await within(basic).findByRole('alert')).toHaveTextContent(/^Agent 正在回复，等这次回复结束后再切换。$/)
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(within(basic).getByText('未保存')).toBeInTheDocument()
  })
})

describe('Claude Code 不需要账号', () => {
  const account = '这个 Agent 用本机的 Claude Code 和你在本机的登录，不需要在平台配置账号，平台也不保存登录信息。'

  it('平台 Agent：模型账号一节只有说明，没有登录、录入入口，也不查询登录状态', async () => {
    const agent = claudeAgent({ scope: 'platform', projectId: null })
    let authReads = 0
    await renderConfig({ agent, handlers: [catalog, http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => { authReads += 1; return HttpResponse.json({ credential: null, operation: null }) })] })
    const section = region('模型账号凭证')
    expect(section).toHaveTextContent(account)
    expect(within(section).queryAllByRole('button')).toEqual([])
    expect(within(section).queryByText('未设置')).toBeNull()
    expect(authReads).toBe(0)
  })

  it('项目 Agent：不显示 Claude Code 的账号来源与去项目设置的入口', async () => {
    // 默认的账号来源里有 claude-code 这一项（没有候选账号），与服务端一致。
    await renderConfig({ agent: claudeAgent(), handlers: [catalog] })
    const section = region('模型账号凭证')
    expect(section).toHaveTextContent(account)
    expect(section).not.toHaveTextContent('模型账号由项目维护')
    await waitFor(() => expect(within(section).queryByRole('status')).toBeNull())
    expect(within(section).queryByRole('combobox')).toBeNull()
    expect(within(section).queryByRole('link')).toBeNull()
  })
})

describe('本机的 Claude Code 不可用或没登录', () => {
  const runtimes = (list: Schemas['RuntimeList']) => {
    let reads = 0
    return { handler: http.get(apiUrl('/runtimes'), () => { reads += 1; return HttpResponse.json(list) }), reads: () => reads }
  }

  it('选中时提醒，说明怎么处理、处理好之前运行会失败；仍然可以选并保存', async () => {
    const agent = makeAgent()
    const status = runtimes(runtimeList({ available: false, version: null, binaryPath: null, unavailableReason: 'not_found', login: null }))
    const bodies: unknown[] = []
    const { user } = await renderConfig({
      agent,
      handlers: [catalog, status.handler, http.patch(apiUrl(`/agents/${agent.id}`), async ({ request }) => { bodies.push(await request.json()); return HttpResponse.json(claudeAgent({ id: agent.id })) })],
    })
    const basic = region('基本信息')
    const select = within(basic).getByLabelText('模型提供方')
    await waitFor(() => expect(option(select, 'claude-code')).toBeDefined())
    // 没选 Claude Code 时不读运行时状态。
    expect(status.reads()).toBe(0)
    await user.selectOptions(select, 'claude-code')
    expect(await within(basic).findByText('本机没找到 claude：装好 Claude Code，并确认在终端里能运行 claude。处理好之前，用它的 Agent 运行会失败。')).toHaveAttribute('role', 'note')
    await chooseModel(user, basic, 'sonnet')
    await user.click(saveButton())
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: '切换并保存' }))
    await waitFor(() => expect(bodies).toEqual([{ model: { provider: 'claude-code', modelId: 'sonnet' } }]))
  })

  it('没登录时提醒在本机终端登录；已登录或登录状态判断不了时不提醒', async () => {
    const loggedOut = runtimes(runtimeList({ login: { state: 'logged_out', authMethod: null, subscriptionType: null } }))
    const first = await renderConfig({ agent: claudeAgent(), handlers: [catalog, loggedOut.handler] })
    expect(await within(region('基本信息')).findByText('本机的 Claude Code 没有登录：在本机终端运行 claude 并登录。处理好之前，用它的 Agent 运行会失败。')).toBeInTheDocument()
    first.unmount()

    for (const login of [{ state: 'logged_in', authMethod: 'claude.ai', subscriptionType: 'max' }, { state: 'unknown', authMethod: null, subscriptionType: null }] as const) {
      const status = runtimes(runtimeList({ login }))
      const view = await renderConfig({ agent: claudeAgent(), handlers: [catalog, status.handler] })
      await waitFor(() => expect(status.reads()).toBe(1))
      await new Promise((resolve) => setTimeout(resolve, 20))
      expect(within(region('基本信息')).queryByText(/处理好之前，用它的 Agent 运行会失败/)).toBeNull()
      view.unmount()
    }
  })
})
