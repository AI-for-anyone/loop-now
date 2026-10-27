import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { apiUrl, defaultProject, makeProject } from '@/test/api'
import { fakePlugins, makeConfig, makeDelivery, makeProjectPlugin } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const other = makeProject({ name: 'loop now scalable' })

function renderDetail(path = '/plugins/webhook', initial: Parameters<typeof fakePlugins>[0] = {}) {
  const fake = fakePlugins({
    configs: { webhook: makeConfig({ values: { url: 'https://hooks.example.com/loop-now', retries: 5 } as never, secretsSet: ['secret'], revision: 2 }) },
    projects: { [defaultProject.id]: { webhook: makeProjectPlugin({ enabled: true, config: makeConfig({ values: { url: 'https://hooks.example.com/propanda' } as never, revision: 3 }) }) } },
    deliveries: {
      webhook: [
        makeDelivery({ eventId: 'e4', at: '2026-10-27T04:41:00Z', durationMs: 120 }),
        makeDelivery({ eventId: 'e3', at: '2026-10-27T04:12:00Z', status: 'failed', attempts: 3, lastError: 'timeout after 15000ms', durationMs: null, ackedAt: null }),
        ...Array.from({ length: 60 }, (_, i) => makeDelivery({ eventId: `old-${i}`, at: '2026-10-26T01:00:00Z' })),
      ],
    },
    state: {
      webhook: [
        { key: 'deliveries/01A', value: { id: '01A', at: '2026-10-27T04:41:00Z', eventId: 'e4', eventType: 'inbox.received', target: 'https://hooks.example.com/loop-now', status: 'delivered', attempts: 1, nextAttemptAt: null, lastStatusCode: 200, lastError: null, updatedAt: '2026-10-27T04:41:01Z' } },
        { key: 'deliveries/01B', value: { id: '01B', at: '2026-10-26T22:10:00Z', eventId: 'e1', eventType: 'inbox.received', target: 'https://old.example.com/hook', status: 'abandoned', attempts: 5, nextAttemptAt: null, lastStatusCode: 404, lastError: 'Not Found', updatedAt: '2026-10-27T01:00:00Z' } },
        { key: 'deliveries/01C', value: { id: '01C', at: '2026-10-27T04:12:00Z', eventId: 'e3', eventType: 'inbox.received', target: 'https://hooks.example.com/loop-now', status: 'failed', attempts: 3, nextAttemptAt: '2026-10-27T04:17:00Z', lastStatusCode: 503, lastError: '503', updatedAt: '2026-10-27T04:12:30Z' } },
        { key: 'deliveries/legacy', value: { note: 'written by an older version' } },
      ],
    },
    logs: { webhook: Array.from({ length: 120 }, (_, i) => ({ id: 200 - i, at: '2026-10-27T04:41:02Z', level: i === 1 ? 'warn' : 'info', line: i === 0 ? 'delivered evt_01JB → hooks.example.com 200 in 118ms' : `line ${i}` })) },
    ...initial,
  })
  server.use(...fake.handlers, ...baseHandlers(), http.get(apiUrl('/projects'), () => HttpResponse.json({ projects: [defaultProject, other] })))
  return { ...renderRoutes(routes, path), fake }
}

describe('插件详情', () => {
  it('页头：名称、官方来源、版本与状态；页签写在地址里；概览列出组件、启用项目、最近事件、投递记录与日志', async () => {
    renderDetail()
    expect(await screen.findByRole('heading', { level: 1, name: 'Webhook 通知' })).toBeInTheDocument()
    const tabs = screen.getByRole('navigation', { name: 'Webhook 通知' })
    expect(within(tabs).getAllByRole('link').map((link) => [link.textContent, link.getAttribute('aria-current')])).toEqual([
      ['概览', 'page'],
      ['配置', null],
      ['事件', null],
      ['投递记录', null],
      ['日志', null],
    ])
    expect(screen.getByRole('navigation', { name: '页面位置' })).toHaveTextContent('全局 · 插件市场 › webhook')
    const contains = screen.getByRole('region', { name: '这个插件包含' })
    expect(within(contains).getByText('inbox.read').parentElement).toHaveTextContent('读收件箱消息正文')
    const enabled = screen.getByRole('region', { name: '在哪些项目启用' })
    await waitFor(() => expect(within(enabled).getByRole('switch', { name: `在 ${defaultProject.name} 启用 Webhook 通知` })).toHaveAttribute('aria-checked', 'true'))
    expect(within(enabled).getByText(defaultProject.name).closest('li')).toHaveTextContent('回调地址 → https://hooks.example.com/propanda')
    const events = screen.getByRole('region', { name: '最近事件' })
    await waitFor(() => expect(within(events).getAllByRole('listitem')).toHaveLength(4))
    expect(within(events).getAllByRole('listitem')[0]).toHaveTextContent('已处理 · 120 ms')
    expect(within(events).getAllByRole('listitem')[1]).toHaveTextContent('处理失败 · 尝试 3 次timeout after 15000ms')
    const logs = screen.getByRole('region', { name: '日志' })
    await waitFor(() => expect(within(logs).getAllByRole('listitem')).toHaveLength(3))
  })

  it('在另一个项目启用：带上它现有的项目级配置与版本', async () => {
    const user = userEvent.setup()
    const { fake } = renderDetail()
    const toggle = await screen.findByRole('switch', { name: `在 ${other.name} 启用 Webhook 通知` })
    await waitFor(() => expect(toggle).toBeEnabled())
    await user.click(toggle)
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'PUT', path: `/projects/${other.id}/plugins/webhook`, query: {}, body: { enabled: true, values: {}, expectedRevision: 0 } }]))
  })

  it('配置页签：按清单生成的全局表单，密钥已设置；保存带版本；项目级覆盖与给插件进程的环境', async () => {
    const user = userEvent.setup()
    const { fake } = renderDetail('/plugins/webhook/config')
    expect(await screen.findByLabelText('回调地址')).toHaveValue('https://hooks.example.com/loop-now')
    expect(screen.getByText('已设置')).toBeInTheDocument()
    await user.clear(screen.getByLabelText('重试上限'))
    await user.type(screen.getByLabelText('重试上限'), '3')
    await user.click(screen.getByRole('button', { name: '保存' }))
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'PUT', path: '/plugins/webhook/config', query: {}, body: { values: { url: 'https://hooks.example.com/loop-now', retries: 3 }, expectedRevision: 2 } }]))
    const overrides = screen.getByRole('region', { name: '项目级覆盖' })
    await waitFor(() => expect(overrides).toHaveTextContent(`${defaultProject.name}覆盖 1 项回调地址 → https://hooks.example.com/propanda`))
    expect(screen.getByRole('region', { name: '给插件进程的环境' })).toHaveTextContent('LOOP_NOW_PLUGIN_DATA=/work/platform/plugins/webhook/data')
    expect(screen.getByRole('region', { name: '给插件进程的环境' })).toHaveTextContent('只含 inbox.read、state')
  })

  it('事件页签：按状态筛选，往下加载更早的', async () => {
    const user = userEvent.setup()
    const { fake } = renderDetail('/plugins/webhook/events')
    const panel = await screen.findByRole('region', { name: '事件' })
    await waitFor(() => expect(within(panel).getAllByRole('listitem')).toHaveLength(50))
    await user.click(within(panel).getByRole('button', { name: '加载更早的' }))
    await waitFor(() => expect(within(panel).getAllByRole('listitem')).toHaveLength(62))
    await user.click(within(panel).getByRole('radio', { name: '处理失败' }))
    await waitFor(() => expect(within(panel).getAllByRole('listitem')).toHaveLength(1))
    expect(fake.requests.filter((r) => r.path === '/plugins/webhook/deliveries').map((r) => r.query)).toEqual([{ limit: '50' }, { limit: '50', before: 'old-47' }, { limit: '50', status: 'failed' }])
  })

  it('投递记录页签：按约定的记录形状列出，新的在前；重试中写下次时间，放弃写状态码；认不得的显示原始 JSON', async () => {
    renderDetail('/plugins/webhook/records')
    const panel = await screen.findByRole('region', { name: '投递记录' })
    const rows = await within(panel).findAllByRole('row')
    expect(rows.slice(1).map((row) => row.textContent)).toEqual([
      expect.stringMatching(/hooks\.example\.com\/loop-now2001$/),
      expect.stringMatching(/hooks\.example\.com\/loop-now重试中下次 .*3$/),
      expect.stringMatching(/old\.example\.com\/hook已放弃 · 4045$/),
      expect.stringContaining('deliveries/legacy'),
    ])
    expect(within(rows[4]).getByLabelText('原始记录')).toHaveTextContent('"note": "written by an older version"')
  })

  it('日志页签：一页 100 条，按 nextBefore 往前翻', async () => {
    const user = userEvent.setup()
    renderDetail('/plugins/webhook/logs')
    const panel = await screen.findByRole('region', { name: '日志' })
    await waitFor(() => expect(within(panel).getAllByRole('listitem')).toHaveLength(100))
    expect(within(panel).getAllByRole('listitem')[0]).toHaveTextContent('info delivered evt_01JB → hooks.example.com 200 in 118ms')
    await user.click(within(panel).getByRole('button', { name: '加载更早的日志' }))
    await waitFor(() => expect(within(panel).getAllByRole('listitem')).toHaveLength(120))
    expect(within(panel).queryByRole('button', { name: '加载更早的日志' })).toBeNull()
  })

  it('检查更新：按已装来源取预览，确认后更新到新版本', async () => {
    const user = userEvent.setup()
    const { fake } = renderDetail()
    await user.click(await screen.findByRole('button', { name: '检查更新' }))
    const dialog = await screen.findByRole('dialog', { name: /更新 Webhook 通知/ })
    expect(dialog).toHaveTextContent('webhook · v1.2.0 → v1.3.0')
    await user.click(within(dialog).getByRole('button', { name: '更新' }))
    await waitFor(() => expect(fake.writes().map((r) => [r.method, r.path, r.body])).toEqual([
      ['POST', '/plugin-previews', { pluginId: 'webhook' }],
      ['POST', '/plugins/webhook/update', { previewId: '71000000-0000-7000-8000-000000000001' }],
    ]))
  })

  it('卸载：可选同时删除数据目录，卸载后回到插件市场', async () => {
    const user = userEvent.setup()
    const { fake, router } = renderDetail()
    await user.click(await screen.findByRole('button', { name: '卸载' }))
    const dialog = await screen.findByRole('alertdialog', { name: '卸载 Webhook 通知？' })
    await user.click(within(dialog).getByRole('checkbox', { name: '同时删除数据目录 /work/platform/plugins/webhook/data' }))
    await user.click(within(dialog).getByRole('button', { name: '卸载' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/plugins'))
    expect(fake.writes()).toEqual([{ method: 'DELETE', path: '/plugins/webhook', query: { deleteData: 'true' }, body: undefined }])
  })

  it('没有这个插件时说明并给出回到插件市场的链接', async () => {
    renderDetail('/plugins/missing')
    expect(await screen.findByText('没有这个插件，可能已被卸载。')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '回到插件市场' })).toHaveAttribute('href', '/plugins')
  })
})
