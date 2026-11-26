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

// Project settings › Model accounts: adding an account uses a search panel to choose
// the provider, and each row of the account list shows the same provider name.
const provider = (id: string, modelCount: number, authMethods: Schemas['ModelAuthMethod'][] = ['api_key']): Schemas['ModelProvider'] => ({ id, name: null, pluginId: null, pluginDisplayName: null, unavailableReason: null, usable: null, source: 'builtin', runtime: 'pi', apiKeyEnvVars: [], modelCount, authMethods })
const catalog: Schemas['ModelProvider'][] = [
  provider('anthropic', 17), provider('deepseek', 2), provider('moonshotai', 4), provider('moonshotai-cn', 4), provider('no-login', 5, []),
  provider('openai-codex', 7, ['oauth']), provider('openrouter', 401), provider('typesafe', 0), provider('zai', 7), provider('zai-coding-cn', 4), provider('zz-new', 3),
  // The local Claude Code: it uses its own login on this machine and has no project account.
  { ...provider('claude-code', 4, []), name: 'Claude Code', source: 'local_cli', runtime: 'claude-code' },
]
const codexAccount = projectAccount({ id: '10000000-0000-7000-8000-000000000002', provider: 'openai-codex', authMethod: 'oauth', status: 'logged_in', name: 'Codex account' })

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

const names = (scope: HTMLElement, role: 'option' | 'button') => within(scope).getAllByRole(role).map(item => item.querySelector('.font-semibold')?.textContent ?? item.textContent)
const option = (name: string) => screen.getByRole('option', { name: new RegExp(`^${name}`) })

describe('Model provider picker', () => {
  it('providers registered by plugins form their own "From plugins" group: shows the extension name, id and plugin, can be searched by plugin name, and still names the plugin once chosen', async () => {
    const { user } = start([], [...catalog, pluginProvider('grok-cli'), pluginProvider('plain-plugin', { name: null, pluginDisplayName: null, pluginId: 'plain' })])
    await user.click(screen.getByRole('button', { name: 'Add model account · provider' }))
    const group = await screen.findByRole('group', { name: 'From plugins' })
    expect(names(group, 'option')).toEqual(['Grok CLI', 'plain-plugin'])
    expect(option('Grok CLI')).toHaveTextContent(/Grok CLIgrok-cliPlugin · Grok subscriptionLogin2 models$/)
    expect(option('plain-plugin')).toHaveTextContent(/Plugin · plain/)
    expect(within(screen.getByRole('group', { name: 'Other' })).queryByRole('option', { name: /grok/ })).toBeNull()
    await user.type(screen.getByRole('searchbox', { name: 'Search providers' }), 'subscription')
    expect(screen.getAllByRole('option').map(item => item.querySelector('.font-semibold')?.textContent)).toEqual(['Grok CLI'])
    await user.click(option('Grok CLI'))
    const chosen = screen.getByRole('group', { name: 'Add model account · provider' })
    expect(chosen).toHaveTextContent('Grok CLI')
    expect(chosen).toHaveTextContent('grok-cli')
    expect(chosen).toHaveTextContent('Plugin · Grok subscription')
  })

  it('opening the panel focuses the search box; common providers are at the top with those that have accounts first; the list is grouped by purpose, and providers without models or that cannot be entered are hidden', async () => {
    const { user } = start()
    await user.click(screen.getByRole('button', { name: 'Add model account · provider' }))
    expect(screen.getByRole('searchbox', { name: 'Search providers' })).toHaveFocus()
    await screen.findByRole('option', { name: /^Anthropic/ })
    const chips = within(screen.getByRole('group', { name: 'Common providers' })).getAllByRole('button').map(chip => chip.textContent)
    expect(chips).toEqual(['OpenAI Codex', 'Anthropic', 'DeepSeek', 'Moonshot Kimi'])

    expect(names(screen.getByRole('group', { name: 'Model makers' }), 'option')).toEqual(['OpenAI Codex', 'Anthropic'])
    expect(names(screen.getByRole('group', { name: 'Chinese providers' }), 'option')).toEqual(['DeepSeek', 'Moonshot Kimi', 'Zhipu Z.ai'])
    expect(names(screen.getByRole('group', { name: 'Aggregators & gateways' }), 'option')).toEqual(['OpenRouter'])
    expect(names(screen.getByRole('group', { name: 'Other' }), 'option')).toEqual(['zz-new'])
    expect(screen.queryByRole('option', { name: /typesafe/ })).toBeNull()
    expect(screen.queryByRole('option', { name: /no-login/ })).toBeNull()
    expect(screen.queryByRole('option', { name: /Claude Code|claude-code/ })).toBeNull()

    expect(option('OpenAI Codex')).toHaveTextContent(/openai-codex.*1 account.*Login.*7 models/)
    expect(option('Anthropic')).toHaveTextContent(/anthropic.*API key.*17 models/)
    // Regional variants collapse into one entry: it shows the first id and how many more
    // there are, and the model count is the largest among the variants, not their sum.
    expect(option('Moonshot Kimi')).toHaveTextContent(/moonshotai \+1.*4 models/)
    expect(within(option('zz-new')).queryByText('zz-new', { selector: '.font-mono' })).toBeNull()
  })

  it('filters as you type: matches the display name, id and aliases; providers without models appear when searching; with no match it says where to add a custom provider', async () => {
    const { user } = start()
    await user.click(screen.getByRole('button', { name: 'Add model account · provider' }))
    await screen.findByRole('option', { name: /^Anthropic/ })
    const search = screen.getByRole('searchbox', { name: 'Search providers' })
    const results = async (query: string) => { await user.clear(search); if (query) await user.type(search, query); return screen.queryAllByRole('option').map(item => item.querySelector('.font-semibold')?.textContent) }
    expect(await results('z.ai')).toEqual(['Zhipu Z.ai'])
    expect(await results('zhipu')).toEqual(['Zhipu Z.ai'])
    expect(await results('kimi')).toEqual(['Moonshot Kimi'])
    expect(await results('moonshot')).toEqual(['Moonshot Kimi'])
    expect(await results('moonshotai-cn')).toEqual(['Moonshot Kimi'])
    expect(await results('claude')).toEqual(['Anthropic'])
    expect(screen.queryByRole('group', { name: 'Common providers' })).toBeNull()
    expect(await results('typesafe')).toEqual(['typesafe'])
    expect(await results('no such vendor')).toEqual([])
    expect(screen.getByRole('status')).toHaveTextContent('No matching provider. To add a custom provider, edit models.json in global settings.')
    expect(await results('')).toContain('Anthropic')
    expect(screen.getByRole('group', { name: 'Common providers' })).toBeInTheDocument()
  })

  it('regional variants: once chosen, a segmented control in the chosen bar switches the region, filled-in values are kept, and the account gets the specific id; a key provider shows the form directly with a "Save key" button', async () => {
    const { user, bodies } = start()
    await user.click(screen.getByRole('button', { name: 'Add model account · provider' }))
    await user.click(await screen.findByRole('option', { name: /^Moonshot Kimi/ }))
    expect(screen.queryByRole('listbox')).toBeNull()
    const bar = screen.getByRole('group', { name: 'Add model account · provider' })
    expect(bar).toHaveTextContent('Moonshot Kimi')
    expect(bar).toHaveTextContent('moonshotai')
    expect(within(bar).getByRole('radio', { name: 'International' })).toBeChecked()
    expect(within(bar).getByRole('button', { name: 'Change' })).toHaveFocus()
    await user.type(screen.getByLabelText('Account name'), 'Kimi China'); await user.type(screen.getByLabelText('API Key'), 'variant-secret')
    await user.click(within(bar).getByRole('radio', { name: 'China' }))
    expect(bar).toHaveTextContent('moonshotai-cn')
    expect(screen.getByLabelText('API Key')).toHaveValue('variant-secret')
    expect(await screen.findByText(/No matching users right now/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Save key' }))
    await waitFor(() => expect(bodies).toEqual([{ purpose: 'model', provider: 'moonshotai-cn', name: 'Kimi China', credential: { type: 'api_key', key: 'variant-secret' } }]))
    // After saving, it collapses back to the trigger button.
    expect(await screen.findByRole('button', { name: 'Add model account · provider' })).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByLabelText('API Key')).toBeNull()
  })

  it('when "Change" reopens the panel the form is hidden first and comes back unchanged after Esc; Cancel in the form returns to the panel; a login provider has a "Log in to X" button', async () => {
    const { user } = start()
    await user.click(screen.getByRole('button', { name: 'Add model account · provider' }))
    await user.click(await screen.findByRole('option', { name: /^Anthropic/ }))
    await user.type(screen.getByLabelText('API Key'), 'kept-secret')
    await user.click(screen.getByRole('button', { name: 'Change' }))
    expect(screen.getByRole('searchbox', { name: 'Search providers' })).toHaveFocus()
    expect(screen.getByLabelText('API Key')).not.toBeVisible()
    await user.keyboard('{Escape}')
    expect(screen.getByLabelText('API Key')).toBeVisible()
    expect(screen.getByLabelText('API Key')).toHaveValue('kept-secret')
    expect(screen.getByRole('button', { name: 'Change' })).toHaveFocus()

    await user.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(screen.getByRole('searchbox', { name: 'Search providers' })).toHaveFocus()
    await user.click(screen.getByRole('option', { name: /^OpenAI Codex/ }))
    expect(await screen.findByRole('button', { name: 'Log in to OpenAI Codex' })).toBeInTheDocument()
    expect(screen.queryByLabelText('API Key')).toBeNull()
  })

  it('keyboard: arrow keys move (the search box points at the current item), Enter chooses, Esc closes and returns focus to the button', async () => {
    const { user } = start()
    const trigger = screen.getByRole('button', { name: 'Add model account · provider' })
    await user.click(trigger)
    await screen.findByRole('option', { name: /^Anthropic/ })
    const search = screen.getByRole('searchbox', { name: 'Search providers' })
    const current = () => document.getElementById(search.getAttribute('aria-activedescendant') ?? '')?.querySelector('.font-semibold')?.textContent
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
    expect(screen.getByRole('listbox', { name: 'Providers' })).toBeInTheDocument()
    await user.type(screen.getByRole('searchbox', { name: 'Search providers' }), 'zai')
    await user.keyboard('{ArrowDown}{Enter}')
    expect(screen.getByRole('group', { name: 'Add model account · provider' })).toHaveTextContent(/Zhipu Z\.ai.*zai/)
    expect(screen.getByRole('radio', { name: 'China Coding' })).not.toBeChecked()
  })

  it('each account row shows the provider name, id, auth method and the update time in the local format; an id not in the table is shown as is', async () => {
    start([codexAccount, projectAccount({ provider: 'zz-new', name: 'New provider account' })])
    const codex = (await screen.findByRole('heading', { name: 'Codex account' })).closest('li')!
    expect(within(codex).getByText('OpenAI Codex')).toBeInTheDocument()
    expect(within(codex).getByText('openai-codex')).toBeInTheDocument()
    expect(within(codex).getByText('Login')).toBeInTheDocument()
    const updated = codex.querySelector('time')!
    expect(updated).toHaveAttribute('dateTime', codexAccount.updatedAt)
    expect(updated).toHaveTextContent(enUS.chat.time(codexAccount.updatedAt))
    expect(codex).not.toHaveTextContent(codexAccount.updatedAt)
    const unknown = screen.getByRole('heading', { name: 'New provider account' }).closest('li')!
    expect(within(unknown).getAllByText('zz-new')).toHaveLength(1)
    expect(within(unknown).getByText('API key')).toBeInTheDocument()
  })
})

describe('Provider table', () => {
  it('each id appears only once; an entry with several ids has as many variant names in both Chinese and English, and one with a single id has none', () => {
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
