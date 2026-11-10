import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { apiUrl, defaultProject, makeProject } from '@/test/api'
import { fakePlugins, makeComponents, makeConfig, makeDelivery, makeDetail, makeManifest, makePlugin, makeProjectPlugin, makeResolvedSource } from '@/test/plugins'
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

describe('插件详情的说明与仓库', () => {
  const readme = [
    '# Webhook 通知',
    '',
    '## 配置',
    '',
    '- `url`：回调地址',
    '- `retries`：重试上限',
    '',
    '```json',
    '{ "url": "https://hooks.example.com" }',
    '```',
    '',
    '详见[文档](https://docs.example.com/webhook)、[示例](./examples/basic.md)与[邮件](mailto:dev@example.com)。',
    '',
    '![架构图](./docs/arch.png)![远程图](https://img.example.com/x.png)',
    '',
    '<script>alert(1)</script>',
  ].join('\n')

  it('概览的说明卡片：按控制台语言读 README；渲染标题、列表、代码块、行内代码与链接，原始 HTML 按文字显示；相对链接与图片只显示文字', async () => {
    const { fake } = renderDetail('/plugins/webhook', { readmes: { webhook: { file: 'README.zh-CN.md', markdown: readme, truncated: false } } })
    const card = await screen.findByRole('region', { name: '说明' })
    expect(fake.requests.find((r) => r.path === '/plugins/webhook/readme')?.query).toEqual({ locale: 'zh-CN' })
    expect(card).toHaveTextContent('README.zh-CN.md')
    expect(within(card).getByRole('heading', { level: 2, name: '配置' })).toBeInTheDocument()
    expect(within(card).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['url：回调地址', 'retries：重试上限'])
    expect(within(card).getByText('url').tagName).toBe('CODE')
    expect(card.querySelector('pre code')).toHaveTextContent('{ "url": "https://hooks.example.com" }')
    const docs = within(card).getByRole('link', { name: '文档' })
    expect(docs).toHaveAttribute('href', 'https://docs.example.com/webhook')
    expect(docs).toHaveAttribute('target', '_blank')
    expect(docs.getAttribute('rel')).toContain('noopener')
    expect(within(card).getByRole('link', { name: '邮件' })).toHaveAttribute('href', 'mailto:dev@example.com')
    expect(within(card).queryByRole('link', { name: '示例' })).toBeNull()
    expect(within(card).getByText('示例')).toBeInTheDocument()
    expect(card.querySelector('img')).toBeNull()
    expect(card).toHaveTextContent('架构图远程图')
    expect(card.querySelector('script')).toBeNull()
    expect(card).toHaveTextContent('<script>alert(1)</script>')
    expect(screen.queryByText('说明太长，只显示了开头的部分。')).toBeNull()
  })

  it('README 被截断时卡片底部说明', async () => {
    renderDetail('/plugins/webhook', { readmes: { webhook: { file: 'README.md', markdown: '# 很长的说明', truncated: true } } })
    const card = await screen.findByRole('region', { name: '说明' })
    expect(card.lastElementChild).toHaveTextContent('说明太长，只显示了开头的部分。')
  })

  it('没有 README 时不显示说明卡片', async () => {
    const { client } = renderDetail()
    await screen.findByRole('region', { name: '这个插件包含' })
    await waitFor(() => expect(client.getQueryState(['plugins', 'readme', 'webhook'])?.status).toBe('success'))
    expect(screen.queryByRole('region', { name: '说明' })).toBeNull()
  })

  it('git 来源：页头链到仓库网页，GitHub 带 ref 时链到 /tree/<ref>；SSH 写法只显示地址', async () => {
    const git = (location: string, ref: string | null) => makeDetail(makePlugin({ id: 'lark-notify', displayName: '飞书通知', source: makeResolvedSource({ kind: 'git', location, ref, commit: 'abc1234def' }) }))
    const { unmount } = renderDetail('/plugins/lark-notify', { details: [git('https://github.com/foo/loop-now-lark-notify.git', 'release/0.3')] })
    const link = await screen.findByRole('link', { name: '仓库' })
    expect(link).toHaveAttribute('href', 'https://github.com/foo/loop-now-lark-notify/tree/release/0.3')
    expect(link).toHaveAttribute('target', '_blank')
    expect(link.getAttribute('rel')).toContain('noopener')
    unmount()
    renderDetail('/plugins/lark-notify', { details: [git('git@github.com:foo/loop-now-lark-notify.git', null)] })
    await screen.findByRole('heading', { level: 1, name: '飞书通知' })
    expect(screen.queryByRole('link', { name: '仓库' })).toBeNull()
    expect(screen.getByText('git@github.com:foo/loop-now-lark-notify.git')).toBeInTheDocument()
  })

  it('官方插件没有仓库链接', async () => {
    renderDetail()
    await screen.findByRole('heading', { level: 1, name: 'Webhook 通知' })
    expect(screen.queryByRole('link', { name: '仓库' })).toBeNull()
  })
})

describe('插件详情的模型提供方', () => {
  const grok = makePlugin({ id: 'grok', displayName: 'Grok 订阅' })
  const withProviders = (overrides: Partial<ReturnType<typeof makeDetail>>, agentExtensions = 1) => ({
    ...makeDetail(grok, makeManifest({ id: 'grok', displayName: 'Grok 订阅', components: makeComponents({ agentExtensions }) })),
    ...overrides,
  })
  const provider = (id: string, overrides: Partial<ReturnType<typeof makeDetail>['providers'][number]> = {}) => ({
    id, name: id, extension: 'extensions/grok.ts', oauth: true, overridesBuiltin: false, inCatalog: false, error: null, modelCount: 2, ...overrides,
  })

  it('列出 Pi 扩展注册的提供方：进了目录的标出，没进的说明原因；名称与错误是扩展原文，按纯文本显示；另列加载失败的扩展', async () => {
    renderDetail('/plugins/grok', {
      details: [
        withProviders({
          providers: [
            provider('grok-cli', { name: 'Grok CLI', inCatalog: true, modelCount: 3 }),
            provider('anthropic', { overridesBuiltin: true }),
            provider('grok-key', { oauth: false }),
            provider('grok-bad', { name: '<b>坏</b>', error: 'TypeError: <script>x</script>' }),
            provider('grok-dup'),
            provider('grok-bare', { name: 'Grok Bare', inCatalog: true }),
          ],
          providerErrors: [{ path: 'extensions/broken.ts', error: 'SyntaxError: Unexpected token' }],
          providerState: { 'grok-cli': { paths: ['.grok/auth.json', '.config/grok'] }, 'grok-key': { paths: ['.grok-key'] } },
        }),
      ],
    })
    const panel = await screen.findByRole('region', { name: '模型提供方' })
    expect(panel).toHaveTextContent('安装或更新时从 Pi 扩展读出')
    const items = within(within(panel).getAllByRole('list')[0]).getAllByRole('listitem')
    expect(items.map((item) => item.textContent)).toEqual([
      'Grok CLIgrok-cliOAuth 登录已进模型目录登录状态随项目账号保存：.grok/auth.json、.config/grok3 个模型 · 来自 extensions/grok.ts',
      'anthropicOAuth 登录与 Pi 内置提供方同 id：只覆盖它的设置，不进模型目录2 个模型 · 来自 extensions/grok.ts',
      'grok-key没有注册 OAuth 登录，不进模型目录登录状态随项目账号保存：.grok-key2 个模型 · 来自 extensions/grok.ts',
      '<b>坏</b>grok-badOAuth 登录注册出错：TypeError: <script>x</script>2 个模型 · 来自 extensions/grok.ts',
      'grok-dupOAuth 登录没有进模型目录（例如与 models.json 里的提供方同 id）2 个模型 · 来自 extensions/grok.ts',
      // 进了目录却没声明状态路径：提醒登录可能不随账号保存。
      'Grok Baregrok-bareOAuth 登录已进模型目录没有声明登录状态路径，登录可能不随账号保存2 个模型 · 来自 extensions/grok.ts',
    ])
    expect(panel.querySelector('b, script')).toBeNull()
    const failed = within(panel).getByRole('heading', { name: '加载失败的扩展' }).parentElement!
    expect(failed).toHaveTextContent('extensions/broken.tsSyntaxError: Unexpected token')
    expect(within(panel).queryByText('没有读出模型提供方。')).toBeNull()
  })

  it('整次读取失败时说明原因，不再说"没有读出"', async () => {
    renderDetail('/plugins/grok', { details: [withProviders({ providersError: 'Pi 不可用：超时' })] })
    const panel = await screen.findByRole('region', { name: '模型提供方' })
    expect(within(panel).getByRole('alert')).toHaveTextContent('读取失败：Pi 不可用：超时')
    expect(within(panel).queryByText('没有读出模型提供方。')).toBeNull()
  })

  it('卸载注册了模型提供方的插件：说明订阅账号默认保留、删除数据时一并删除；没有提供方的插件不说', async () => {
    const user = userEvent.setup()
    const { unmount } = renderDetail('/plugins/grok', { details: [withProviders({ providers: [provider('grok-cli', { inCatalog: true })] })] })
    await user.click(await screen.findByRole('button', { name: '卸载' }))
    const dialog = await screen.findByRole('alertdialog', { name: '卸载 Grok 订阅？' })
    expect(dialog).toHaveTextContent('它注册的模型提供方在各项目里的订阅账号默认保留，重新安装后恢复；勾选后这些账号一并删除（被未结束任务用着的保留到任务结束）。')
    unmount()
    renderDetail()
    await user.click(await screen.findByRole('button', { name: '卸载' }))
    expect(await screen.findByRole('alertdialog', { name: '卸载 Webhook 通知？' })).not.toHaveTextContent('订阅账号')
  })

  it('有 Pi 扩展但没有注册提供方：说明没有读出；没有扩展也没读出东西的插件不显示这一块', async () => {
    const { unmount } = renderDetail('/plugins/grok', { details: [withProviders({})] })
    const panel = await screen.findByRole('region', { name: '模型提供方' })
    expect(panel).toHaveTextContent('没有读出模型提供方。')
    unmount()
    renderDetail()
    await screen.findByRole('region', { name: '这个插件包含' })
    expect(screen.queryByRole('region', { name: '模型提供方' })).toBeNull()
  })
})
