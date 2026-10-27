import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { routes } from '@/app/routes'
import { Toasts } from '@/components/Toasts'
import { defaultAgentPath, defaultProject, errorBody, makeAgent } from '@/test/api'
import { fakePlugins, makeAction, makeComponents, makeDetail, makeManifest, makePlugin, makeProjectPlugin } from '@/test/plugins'
import { renderRoutes, renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { PagePluginActions } from './PluginActions'

const resendFields = {
  type: 'object',
  required: ['target'],
  properties: { target: { type: 'string', format: 'uri', title: '目标地址' }, attempts: { type: 'integer', title: '次数', minimum: 1 } },
} as unknown as Record<string, never>

// webhook 带四个操作：全局的“重发”（有表单）、项目的“测试推送”、Agent 的“发一条给它”、任务与外部身份的“同步”。
const webhook = makeDetail(
  makePlugin(),
  makeManifest({
    components: makeComponents({
      actions: [
        makeAction({ id: 'resend', label: '重发', where: ['global'], fields: resendFields }),
        makeAction({ id: 'ping', label: '测试推送', where: ['project'] }),
        makeAction({ id: 'poke', label: '发一条给它', where: ['agent'] }),
        makeAction({ id: 'sync', label: '同步', where: ['task', 'identity'] }),
      ],
    }),
  }),
)

function setup(initial: Parameters<typeof fakePlugins>[0] = {}, agent = makeAgent()) {
  const fake = fakePlugins({ details: [webhook], projects: { [defaultProject.id]: { webhook: makeProjectPlugin({ enabled: true }) } }, agents: { [agent.id]: [{ pluginId: 'webhook', displayName: 'Webhook 通知', available: true, granted: true, accountIds: [], accountKinds: [] }] }, ...initial })
  server.use(...fake.handlers, ...baseHandlers([agent]))
  return { fake, agent }
}

describe('插件的操作按钮', () => {
  it('全局操作在插件详情的页头：按 fields 生成表单，校验后提交 where 与 input；结果以轻提示显示并打开链接', async () => {
    const user = userEvent.setup()
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const { fake } = setup({ actions: { 'webhook/resend': () => HttpResponse.json({ notice: '已重发 3 条', url: 'https://hooks.example.com/log' }) } })
    renderRoutes(routes, '/plugins/webhook')
    const group = await screen.findByRole('group', { name: '插件 Webhook 通知 的操作' })
    expect(within(group).getAllByRole('button').map((b) => b.textContent)).toEqual(['重发'])
    await user.click(within(group).getByRole('button', { name: '重发' }))
    const dialog = await screen.findByRole('dialog', { name: '重发' })
    await user.click(within(dialog).getByRole('button', { name: '运行' }))
    expect(within(dialog).getByText('必填', { selector: '.font-bold' })).toBeInTheDocument()
    await user.type(within(dialog).getByLabelText('目标地址'), 'https://hooks.example.com/x')
    await user.type(within(dialog).getByLabelText('次数'), '2')
    await user.click(within(dialog).getByRole('button', { name: '运行' }))
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'POST', path: '/plugins/webhook/actions/resend', query: {}, body: { where: {}, input: { target: 'https://hooks.example.com/x', attempts: 2 } } }]))
    const toast = await screen.findByText('已重发 3 条')
    expect(within(toast).getByRole('link', { name: '打开' })).toHaveAttribute('href', 'https://hooks.example.com/log')
    expect(open).toHaveBeenCalledWith('https://hooks.example.com/log', '_blank', 'noopener,noreferrer')
    expect(screen.queryByRole('dialog', { name: '重发' })).toBeNull()
    open.mockRestore()
  })

  it.each([
    [502, 'plugin_failed', '插件处理时出错'],
    [504, 'plugin_timeout', '插件没有及时回应'],
  ] as const)('%s %s：弹窗里写出原因，弹窗保持打开', async (status, code, message) => {
    const user = userEvent.setup()
    setup({ actions: { 'webhook/resend': () => HttpResponse.json(errorBody(code, code), { status }) } })
    renderRoutes(routes, '/plugins/webhook')
    await user.click(await screen.findByRole('button', { name: '重发' }))
    const dialog = await screen.findByRole('dialog', { name: '重发' })
    await user.type(within(dialog).getByLabelText('目标地址'), 'https://hooks.example.com/x')
    await user.click(within(dialog).getByRole('button', { name: '运行' }))
    expect(await within(dialog).findByText(message)).toBeInTheDocument()
  })

  it('422 的字段错误标到表单项；409 plugin_not_enabled 说明要先启用', async () => {
    const user = userEvent.setup()
    let calls = 0
    setup({
      actions: {
        'webhook/resend': () =>
          ++calls === 1
            ? HttpResponse.json(errorBody('validation_failed', 'invalid', { fields: [{ field: 'input.target', message: '这个地址不在白名单里' }] }), { status: 422 })
            : HttpResponse.json(errorBody('plugin_not_enabled', 'not enabled'), { status: 409 }),
      },
    })
    renderRoutes(routes, '/plugins/webhook')
    await user.click(await screen.findByRole('button', { name: '重发' }))
    const dialog = await screen.findByRole('dialog', { name: '重发' })
    await user.type(within(dialog).getByLabelText('目标地址'), 'https://hooks.example.com/x')
    await user.click(within(dialog).getByRole('button', { name: '运行' }))
    expect(await within(dialog).findByText('这个地址不在白名单里')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: '运行' }))
    expect(await within(dialog).findByText('这个插件在这里还没有启用，先在项目里启用。')).toBeInTheDocument()
  })

  it('项目操作在项目插件卡片上，where 是这个项目；没有字段的操作只确认一下', async () => {
    const user = userEvent.setup()
    const { fake } = setup()
    renderRoutes(routes, `/p/${encodeURIComponent(defaultProject.name)}/plugins`)
    const card = (await screen.findByRole('link', { name: 'Webhook 通知' })).closest('li')!
    await user.click(await within(card).findByRole('button', { name: '测试推送' }))
    const dialog = await screen.findByRole('dialog', { name: '测试推送' })
    expect(dialog).toHaveTextContent('运行“测试推送”？')
    await user.click(within(dialog).getByRole('button', { name: '运行' }))
    await waitFor(() => expect(fake.writes().at(-1)).toEqual({ method: 'POST', path: '/plugins/webhook/actions/ping', query: {}, body: { where: { projectId: defaultProject.id }, input: {} } }))
    expect(await screen.findByText('已完成')).toBeInTheDocument()
  })

  it('Agent 操作在 Agent 配置页的插件行里（展开后），where 是这个 Agent', async () => {
    const user = userEvent.setup()
    const { fake, agent } = setup()
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const section = await screen.findByRole('region', { name: '插件权限' })
    await user.click(await within(section).findByRole('button', { name: /Webhook 通知/ }))
    await user.click(await within(section).findByRole('button', { name: '发一条给它' }))
    await user.click(within(await screen.findByRole('dialog', { name: '发一条给它' })).getByRole('button', { name: '运行' }))
    await waitFor(() => expect(fake.writes().at(-1)?.body).toEqual({ where: { agentId: agent.id }, input: {} }))
  })

  it('任务页、外部身份页：列出有这种操作的插件；任务页只列项目启用了的', async () => {
    const user = userEvent.setup()
    const other = makeDetail(makePlugin({ id: 'other', displayName: '另一个' }), makeManifest({ id: 'other', displayName: '另一个', components: makeComponents({ actions: [makeAction({ id: 'sync', label: '同步', where: ['task', 'identity'] })] }) }))
    const fake = fakePlugins({ details: [webhook, other], projects: { [defaultProject.id]: { webhook: makeProjectPlugin({ enabled: true }) } } })
    server.use(...fake.handlers)
    const task = renderWithQuery(<><PagePluginActions page="task" where={{ taskId: '73000000-0000-7000-8000-000000000001' }} projectId={defaultProject.id} /><Toasts /></>)
    await screen.findByRole('group', { name: '插件 Webhook 通知 的操作' })
    await waitFor(() => expect(screen.queryByRole('group', { name: '插件 另一个 的操作' })).toBeNull())
    await user.click(screen.getByRole('button', { name: '同步' }))
    await user.click(within(await screen.findByRole('dialog', { name: '同步' })).getByRole('button', { name: '运行' }))
    await waitFor(() => expect(fake.writes().at(-1)?.body).toEqual({ where: { taskId: '73000000-0000-7000-8000-000000000001' }, input: {} }))
    task.unmount()
    renderWithQuery(<PagePluginActions page="identity" where={{ identityId: '74000000-0000-7000-8000-000000000001' }} />)
    expect(await screen.findByRole('group', { name: '插件 另一个 的操作' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: '插件 Webhook 通知 的操作' })).toBeInTheDocument()
  })
})

