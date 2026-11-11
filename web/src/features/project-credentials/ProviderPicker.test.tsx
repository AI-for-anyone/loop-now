import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject } from '@/test/api'
import { projectAccount } from '@/test/project-credentials'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { enUS } from '@/i18n/en-US'
import { zhCN } from '@/i18n/zh-CN'
import { ProjectAccounts } from './ProjectAccounts'
import { providerCatalog } from './provider-catalog'
import { pluginProvider } from '@/test/config'

// 项目设置 › 模型账号：添加账号时用搜索面板选提供方，账号列表的每一行显示同样的提供方名称。
const provider = (id: string, modelCount: number, authMethods: Schemas['ModelAuthMethod'][] = ['api_key']): Schemas['ModelProvider'] => ({ id, name: null, pluginId: null, pluginDisplayName: null, unavailableReason: null, source: 'builtin', runtime: 'pi', apiKeyEnvVars: [], modelCount, authMethods })
const catalog: Schemas['ModelProvider'][] = [
  provider('anthropic', 17), provider('deepseek', 2), provider('moonshotai', 4), provider('moonshotai-cn', 4), provider('no-login', 5, []),
  provider('openai-codex', 7, ['oauth']), provider('openrouter', 401), provider('typesafe', 0), provider('zai', 7), provider('zai-coding-cn', 4), provider('zz-new', 3),
  // 本机的 Claude Code：用它自己在本机的登录，没有项目账号。
  { ...provider('claude-code', 4, []), name: 'Claude Code', source: 'local_cli', runtime: 'claude-code' },
]
const codexAccount = projectAccount({ id: '10000000-0000-7000-8000-000000000002', provider: 'openai-codex', authMethod: 'oauth', status: 'logged_in', name: 'Codex 账号' })

function start(accounts: Schemas['ProjectCredentialAccount'][] = [codexAccount], providers: Schemas['ModelProvider'][] = catalog) {
  const bodies: unknown[] = []
  server.use(
    http.get(apiUrl('/model-providers'), () => HttpResponse.json({ piVersion: '1.4.0', catalogGeneratedAt: null, providers })),
    http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
    http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts })),
    http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accountId: null, agents: [] })),
    http.post(apiUrl('/projects/:projectId/credential-accounts'), async ({ request }) => { bodies.push(await request.json()); return HttpResponse.json(projectAccount(), { status: 201 }) }),
  )
  renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="model" /></DraftNavigationGuard> }], '/')
  return { user: userEvent.setup(), bodies }
}

const names = (scope: HTMLElement, role: 'option' | 'button') => within(scope).getAllByRole(role).map(item => item.querySelector('.font-bold')?.textContent ?? item.textContent)
const option = (name: string) => screen.getByRole('option', { name: new RegExp(`^${name}`) })

describe('模型提供方选择器', () => {
  it('插件注册的提供方单独成"插件提供"一组：显示扩展给的名称、id 和插件，可按插件名搜索，选中后同样标出插件', async () => {
    const { user } = start([], [...catalog, pluginProvider('grok-cli'), pluginProvider('plain-plugin', { name: null, pluginDisplayName: null, pluginId: 'plain' })])
    await user.click(screen.getByRole('button', { name: '添加模型账号 · 提供方' }))
    const group = await screen.findByRole('group', { name: '插件提供' })
    expect(names(group, 'option')).toEqual(['Grok CLI', 'plain-plugin'])
    expect(option('Grok CLI')).toHaveTextContent(/Grok CLIgrok-cli插件 · Grok 订阅登录2 个模型$/)
    expect(option('plain-plugin')).toHaveTextContent(/插件 · plain/)
    expect(within(screen.getByRole('group', { name: '其他' })).queryByRole('option', { name: /grok/ })).toBeNull()
    await user.type(screen.getByRole('searchbox', { name: '搜索提供方' }), '订阅')
    expect(screen.getAllByRole('option').map(item => item.querySelector('.font-bold')?.textContent)).toEqual(['Grok CLI'])
    await user.click(option('Grok CLI'))
    const chosen = screen.getByRole('group', { name: '添加模型账号 · 提供方' })
    expect(chosen).toHaveTextContent('Grok CLI')
    expect(chosen).toHaveTextContent('grok-cli')
    expect(chosen).toHaveTextContent('插件 · Grok 订阅')
  })

  it('打开面板时搜索框聚焦；常用提供方在顶部，已有账号的排最前；列表按用途分组，没有模型的、不能录入的不显示', async () => {
    const { user } = start()
    await user.click(screen.getByRole('button', { name: '添加模型账号 · 提供方' }))
    expect(screen.getByRole('searchbox', { name: '搜索提供方' })).toHaveFocus()
    await screen.findByRole('option', { name: /^Anthropic/ })
    const chips = within(screen.getByRole('group', { name: '常用提供方' })).getAllByRole('button').map(chip => chip.textContent)
    expect(chips).toEqual(['OpenAI Codex', 'Anthropic', 'DeepSeek', 'Moonshot Kimi'])

    expect(names(screen.getByRole('group', { name: '模型厂商' }), 'option')).toEqual(['OpenAI Codex', 'Anthropic'])
    expect(names(screen.getByRole('group', { name: '国内厂商' }), 'option')).toEqual(['DeepSeek', 'Moonshot Kimi', '智谱 Z.ai'])
    expect(names(screen.getByRole('group', { name: '聚合与网关' }), 'option')).toEqual(['OpenRouter'])
    expect(names(screen.getByRole('group', { name: '其他' }), 'option')).toEqual(['zz-new'])
    expect(screen.queryByRole('option', { name: /typesafe/ })).toBeNull()
    expect(screen.queryByRole('option', { name: /no-login/ })).toBeNull()
    expect(screen.queryByRole('option', { name: /Claude Code|claude-code/ })).toBeNull()

    expect(option('OpenAI Codex')).toHaveTextContent(/openai-codex.*已有 1 个.*登录.*7 个模型/)
    expect(option('Anthropic')).toHaveTextContent(/anthropic.*API 密钥.*17 个模型/)
    // 地区变体收成一条：显示第一个 id 和还有几个，模型数取各变体中最多的，不相加。
    expect(option('Moonshot Kimi')).toHaveTextContent(/moonshotai \+1.*4 个模型/)
    expect(within(option('zz-new')).queryByText('zz-new', { selector: '.font-mono' })).toBeNull()
  })

  it('输入即筛：匹配中文名、英文名、id 和别名；没有模型的搜索时出现；没有匹配时说明去哪加自定义 Provider', async () => {
    const { user } = start()
    await user.click(screen.getByRole('button', { name: '添加模型账号 · 提供方' }))
    await screen.findByRole('option', { name: /^Anthropic/ })
    const search = screen.getByRole('searchbox', { name: '搜索提供方' })
    const results = async (query: string) => { await user.clear(search); if (query) await user.type(search, query); return screen.queryAllByRole('option').map(item => item.querySelector('.font-bold')?.textContent) }
    expect(await results('智谱')).toEqual(['智谱 Z.ai'])
    expect(await results('zhipu')).toEqual(['智谱 Z.ai'])
    expect(await results('kimi')).toEqual(['Moonshot Kimi'])
    expect(await results('月之暗面')).toEqual(['Moonshot Kimi'])
    expect(await results('moonshotai-cn')).toEqual(['Moonshot Kimi'])
    expect(await results('claude')).toEqual(['Anthropic'])
    expect(screen.queryByRole('group', { name: '常用提供方' })).toBeNull()
    expect(await results('typesafe')).toEqual(['typesafe'])
    expect(await results('没有这家')).toEqual([])
    expect(screen.getByRole('status')).toHaveTextContent('没有匹配的提供方。要加自定义 Provider，去全局设置的 models.json。')
    expect(await results('')).toContain('Anthropic')
    expect(screen.getByRole('group', { name: '常用提供方' })).toBeInTheDocument()
  })

  it('地区变体：选中后在选中条里用分段按钮切区域，已填的内容保留，写入账号的是具体 id；密钥型直接出表单，按钮是"保存密钥"', async () => {
    const { user, bodies } = start()
    await user.click(screen.getByRole('button', { name: '添加模型账号 · 提供方' }))
    await user.click(await screen.findByRole('option', { name: /^Moonshot Kimi/ }))
    expect(screen.queryByRole('listbox')).toBeNull()
    const bar = screen.getByRole('group', { name: '添加模型账号 · 提供方' })
    expect(bar).toHaveTextContent('Moonshot Kimi')
    expect(bar).toHaveTextContent('moonshotai')
    expect(within(bar).getByRole('radio', { name: '国际' })).toBeChecked()
    expect(within(bar).getByRole('button', { name: '更换' })).toHaveFocus()
    await user.type(screen.getByLabelText('账号名称'), 'Kimi 中国'); await user.type(screen.getByLabelText('API Key'), 'variant-secret')
    await user.click(within(bar).getByRole('radio', { name: '中国' }))
    expect(bar).toHaveTextContent('moonshotai-cn')
    expect(screen.getByLabelText('API Key')).toHaveValue('variant-secret')
    expect(await screen.findByText(/当前没有匹配的使用者/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '保存密钥' }))
    await waitFor(() => expect(bodies).toEqual([{ purpose: 'model', provider: 'moonshotai-cn', name: 'Kimi 中国', credential: { type: 'api_key', key: 'variant-secret' } }]))
    // 保存后收起，回到触发按钮。
    expect(await screen.findByRole('button', { name: '添加模型账号 · 提供方' })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByLabelText('API Key')).toBeNull()
  })

  it('"更换"重开面板时表单先藏起来，Esc 收起后原样回来；表单里点取消回到面板；登录型的按钮是"登录 X"', async () => {
    const { user } = start()
    await user.click(screen.getByRole('button', { name: '添加模型账号 · 提供方' }))
    await user.click(await screen.findByRole('option', { name: /^Anthropic/ }))
    await user.type(screen.getByLabelText('API Key'), 'kept-secret')
    await user.click(screen.getByRole('button', { name: '更换' }))
    expect(screen.getByRole('searchbox', { name: '搜索提供方' })).toHaveFocus()
    expect(screen.getByLabelText('API Key')).not.toBeVisible()
    await user.keyboard('{Escape}')
    expect(screen.getByLabelText('API Key')).toBeVisible()
    expect(screen.getByLabelText('API Key')).toHaveValue('kept-secret')
    expect(screen.getByRole('button', { name: '更换' })).toHaveFocus()

    await user.click(screen.getByRole('button', { name: '取消' }))
    expect(screen.getByRole('searchbox', { name: '搜索提供方' })).toHaveFocus()
    await user.click(screen.getByRole('option', { name: /^OpenAI Codex/ }))
    expect(await screen.findByRole('button', { name: '登录 OpenAI Codex' })).toBeInTheDocument()
    expect(screen.queryByLabelText('API Key')).toBeNull()
  })

  it('键盘：上下键移动（搜索框指向当前项）、回车选中、Esc 收起并把焦点还给按钮', async () => {
    const { user } = start()
    const trigger = screen.getByRole('button', { name: '添加模型账号 · 提供方' })
    await user.click(trigger)
    await screen.findByRole('option', { name: /^Anthropic/ })
    const search = screen.getByRole('searchbox', { name: '搜索提供方' })
    const current = () => document.getElementById(search.getAttribute('aria-activedescendant') ?? '')?.querySelector('.font-bold')?.textContent
    expect(search).not.toHaveAttribute('aria-activedescendant')
    await user.keyboard('{ArrowDown}')
    expect(current()).toBe('OpenAI Codex')
    expect(option('OpenAI Codex')).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{ArrowDown}{ArrowDown}')
    expect(current()).toBe('DeepSeek')
    await user.keyboard('{ArrowUp}')
    expect(current()).toBe('Anthropic')
    expect(option('OpenAI Codex')).toHaveAttribute('aria-selected', 'false')
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(trigger).toHaveFocus()
    expect(trigger).toHaveAttribute('aria-expanded', 'false')

    await user.keyboard('{Enter}')
    expect(screen.getByRole('listbox', { name: '提供方' })).toBeInTheDocument()
    await user.type(screen.getByRole('searchbox', { name: '搜索提供方' }), 'zai')
    await user.keyboard('{ArrowDown}{Enter}')
    expect(screen.getByRole('group', { name: '添加模型账号 · 提供方' })).toHaveTextContent(/智谱 Z\.ai.*zai/)
    expect(screen.getByRole('radio', { name: '中国 Coding' })).not.toBeChecked()
  })

  it('账号列表每一行显示提供方的名称、id、认证方式和本地格式的更新时间；不在表里的 id 直接显示 id', async () => {
    start([codexAccount, projectAccount({ provider: 'zz-new', name: '新提供方账号' })])
    const codex = (await screen.findByRole('heading', { name: 'Codex 账号' })).closest('li')!
    expect(within(codex).getByText('OpenAI Codex')).toBeInTheDocument()
    expect(within(codex).getByText('openai-codex')).toBeInTheDocument()
    expect(within(codex).getByText('登录')).toBeInTheDocument()
    const updated = codex.querySelector('time')!
    expect(updated).toHaveAttribute('dateTime', codexAccount.updatedAt)
    expect(updated).toHaveTextContent(zhCN.chat.time(codexAccount.updatedAt))
    expect(codex).not.toHaveTextContent(codexAccount.updatedAt)
    const unknown = screen.getByRole('heading', { name: '新提供方账号' }).closest('li')!
    expect(within(unknown).getAllByText('zz-new')).toHaveLength(1)
    expect(within(unknown).getByText('API 密钥')).toBeInTheDocument()
  })
})

describe('提供方小表', () => {
  it('每个 id 只出现一次；收了几个 id 的条目，中英文都有同样多的变体名，只有一个的没有', () => {
    const ids = providerCatalog.flatMap(entry => entry.ids)
    expect(new Set(ids).size).toBe(ids.length)
    for (const table of [zhCN, enUS]) {
      for (const entry of providerCatalog) {
        const copy = table.credentials.providerPicker.entries[entry.key] as { variants?: readonly string[] }
        expect(copy.variants?.length, entry.key).toBe(entry.ids.length > 1 ? entry.ids.length : undefined)
      }
    }
  })
})
