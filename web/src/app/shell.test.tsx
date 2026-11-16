import { focusManager } from '@tanstack/react-query'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { keys } from '@/api/queries'
import { config } from '@/config'
import { localeStorageKey, page as browserPage } from '@/i18n/locale'
import { agentsUrl, apiUrl, defaultAgentPath, defaultAgentsPath, defaultProject, errorBody, makeAgent, platformAgentsUrl, toListItem } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, currentSidebarLinks, scopeSwitcher, sidebar, sidebarLinks } from '@/test/shell'
import { routes } from './routes'
import { chooseModel } from '@/test/model-picker'
import { openInboxStream } from '@/test/inbox'
import { AppLayout } from './AppLayout'
import { FakeEventSource } from '@/test/fake-event-source'

// Global connection notice: told apart from the error messages inside each page by the server address in its description.
const banner = () => screen.queryByText(/The console talks to the server at/)?.closest<HTMLElement>('[role="alert"]') ?? null
const findBanner = async () => (await screen.findByText(/The console talks to the server at/)).closest<HTMLElement>('[role="alert"]')!

// Endpoints for the overview page: usage and anomalies are empty; while
// the overview event stream pushes nothing, the run status stays loading.
const overviewHandlers = () => [
  http.get(apiUrl('/usage'), () => HttpResponse.json({ from: null, to: null, rows: [] })),
  http.get(apiUrl('/anomalies'), () => HttpResponse.json({ items: [], total: 0, nextCursor: null })),
]

const project = `/p/${encodeURIComponent('测试项目')}`
const globalEntries = ['/plugins', '/skills', '/stats', '/identities', '/settings']
const hrefs = () => sidebarLinks().map((link) => link.getAttribute('href'))
const names = () => sidebarLinks().map((link) => link.textContent)

describe('sidebar and page structure', () => {
  it('the root address is the overview of the global scope: Overview is current, followed by projects, platform Agents and the global entries at the bottom', async () => {
    const platform = makeAgent({ name: 'Inspector', scope: 'platform', projectId: null })
    server.use(...overviewHandlers(), http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(platform)] })), ...baseHandlers([makeAgent()]))
    renderRoutes(routes, '/')
    expect(await screen.findByRole('heading', { name: 'Overview', level: 1 })).toBeInTheDocument()
    await within(sidebar()).findByRole('link', { name: 'Inspector' })
    expect(scopeSwitcher('Global')).toBeInTheDocument()
    expect(names()).toEqual(['LOOP NOW', 'Overview', '测试项目', 'Inspector', 'Plugin market', 'Skills library', 'Stats', 'External identities', 'Global settings'])
    expect(hrefs()).toEqual(['/', '/', '/?project=%E6%B5%8B%E8%AF%95%E9%A1%B9%E7%9B%AE', `/platform/agents/${platform.id}`, ...globalEntries])
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['Overview'])
    expect(within(sidebar()).getByRole('list', { name: 'Platform Agents' })).toBeInTheDocument()
  })

  it('the main area has a layer of paper grain on its beige background; the sidebar does not', async () => {
    server.use(...overviewHandlers(), http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [] })), ...baseHandlers([makeAgent()]))
    renderRoutes(routes, '/')
    await screen.findByRole('heading', { name: 'Overview', level: 1 })
    expect(screen.getByRole('main').closest('.riso-grain')).not.toBeNull()
    expect(sidebar().closest('.riso-grain')).toBeNull()
  })

  it('an address that does not exist goes back to the overview', async () => {
    server.use(...overviewHandlers(), ...baseHandlers([makeAgent()]))
    const { router } = renderRoutes(routes, '/no/such/page')
    expect(await screen.findByRole('heading', { name: 'Overview', level: 1 })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/')
  })

  it('in the Skills library only “Skills library” is current, not “Overview”', async () => {
    server.use(...baseHandlers([]))
    renderRoutes(routes, '/skills')
    await screen.findByRole('heading', { name: 'Skills library' })
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['Skills library'])
  })

  it('opening an Agent config page directly: the sidebar shows its project with the Agent selected; the global entries are still reachable in the project', async () => {
    const agent = makeAgent({ name: '开发助手' })
    server.use(...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const list = await within(sidebar()).findByRole('list', { name: 'Agents' })
    await waitFor(() => expect(within(list).getByRole('link', { name: '开发助手' })).toHaveAttribute('aria-current', 'page'))
    expect(scopeSwitcher('测试项目')).toBeInTheDocument()
    expect(names()).toEqual(['LOOP NOW', 'Overview', 'Tasks', 'Project folder', 'Project settings', '开发助手', 'Plugin market', 'Skills library', 'Stats', 'External identities', 'Global settings'])
    expect(hrefs()).toEqual(['/', '/?project=%E6%B5%8B%E8%AF%95%E9%A1%B9%E7%9B%AE', `${project}/tasks`, `${project}/folder`, `${project}/settings`, `${project}/agents/${agent.id}`, ...globalEntries])
    expect(within(sidebar()).queryByRole('link', { name: /Inbox/ })).toBeNull()
    expect(await screen.findByRole('link', { name: 'Config' })).toHaveAttribute('aria-current', 'page')
    // Main-area page header: the location breadcrumb, the title in the display font, and the model.
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('测试项目›Agent›开发助手')
    expect(screen.getByRole('heading', { name: '开发助手', level: 1 })).toBeInTheDocument()
    expect(screen.getByText('anthropic · claude-sonnet-5-5')).toBeInTheDocument()
  })

  it('opening a global page (Plugin market, Global settings) from a project keeps the current project in the sidebar', async () => {
    const agent = makeAgent({ name: '开发助手' })
    server.use(...baseHandlers([agent]))
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentPath(agent.id, 'chat'))
    await within(sidebar()).findByRole('link', { name: '开发助手' })
    await user.click(within(sidebar()).getByRole('link', { name: 'Plugin market' }))
    expect(router.state.location.pathname).toBe('/plugins')
    expect(scopeSwitcher('测试项目')).toBeInTheDocument()
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['Plugin market'])
    await user.click(within(sidebar()).getByRole('link', { name: 'Global settings' }))
    expect(router.state.location.pathname).toBe('/settings')
    expect(scopeSwitcher('测试项目')).toBeInTheDocument()
    expect(within(sidebar()).getByRole('link', { name: '开发助手' })).not.toHaveAttribute('aria-current')
  })

  it('a platform Agent address is in the global scope: the sidebar lists the projects and platform Agents and selects it', async () => {
    const platform = makeAgent({ name: 'Inspector', scope: 'platform', projectId: null })
    server.use(http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(platform)] })), ...baseHandlers([platform]))
    renderRoutes(routes, `/platform/agents/${platform.id}/chat`)
    const list = await within(sidebar()).findByRole('list', { name: 'Platform Agents' })
    await waitFor(() => expect(within(list).getByRole('link', { name: 'Inspector' })).toHaveAttribute('aria-current', 'page'))
    expect(scopeSwitcher('Global')).toBeInTheDocument()
    expect(within(sidebar()).getByRole('list', { name: 'Projects' })).toHaveTextContent('测试项目')
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Platform Agent›Inspector')
  })

  it('the full navigation has no Inbox entry, and the Inbox event stream is still set up (messages for you and items needing you are updated through it)', async () => {
    server.use(...overviewHandlers(), ...baseHandlers([]))
    renderRoutes(routes, '/')
    await screen.findByRole('heading', { name: 'Overview', level: 1 })
    await openInboxStream(3)
    expect(within(sidebar()).queryByRole('link', { name: /Inbox/ })).toBeNull()
  })

  it('opening the address of an Agent Inbox tab directly selects the Agent and shows the Inbox tab', async () => {
    const agent = makeAgent({ name: '开发助手' })
    server.use(http.get(apiUrl(`/agents/${agent.id}/inbox`), () => HttpResponse.json({ messages: [], nextCursor: null })), ...baseHandlers([agent]))
    renderRoutes(routes, `${defaultAgentsPath}/${agent.id}/inbox`)
    const list = await screen.findByRole('list', { name: 'Agents' })
    await waitFor(() => expect(within(list).getByRole('link', { name: /开发助手/ })).toHaveAttribute('aria-current', 'page'))
    const tabs = await screen.findByRole('navigation', { name: '开发助手' })
    expect(within(tabs).getByRole('link', { name: 'Inbox' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByText('No Inbox messages yet.')).toBeInTheDocument()
  })

  it('the Agent “Folder” tab sits between Config and Inbox and opens the one folder of this Agent', async () => {
    const agent = makeAgent({ name: '开发助手' })
    const reads: string[] = []
    server.use(
      http.get(apiUrl(`/agents/${agent.id}/folder/entries`), ({ request }) => {
        reads.push(new URL(request.url).searchParams.get('path') ?? '')
        return HttpResponse.json({ path: '', entries: [{ name: 'AGENTS.md', path: 'AGENTS.md', type: 'file', size: 12, modifiedAt: '2026-10-25T08:00:00Z', symlinkTarget: null }], nextCursor: null })
      }),
      http.get(apiUrl(`/agents/${agent.id}/folder/content`), () => new HttpResponse('# Working notes\n', { headers: { 'Content-Type': 'application/octet-stream' } })),
      ...baseHandlers([agent]),
    )
    renderRoutes(routes, `${defaultAgentsPath}/${agent.id}/folder`)
    const tabs = await screen.findByRole('navigation', { name: '开发助手' })
    expect(within(tabs).getAllByRole('link').map((link) => link.textContent)).toEqual(['Chat', 'Config', 'Folder', 'Inbox'])
    expect(within(tabs).getByRole('link', { name: 'Folder' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByRole('tree', { name: 'File tree' })).toHaveTextContent("开发助手's folder")
    expect(await within(screen.getByRole('region', { name: 'AGENTS.md' })).findByText('Working notes')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Pull from Git' })).toBeInTheDocument()
    expect(reads).toContain('')
    // Redesign: the folder sits directly on the tab's sheet, with the overview in the stub on the
    // right; the description that used to sit at the top of the tab moved into the footer note.
    expect(screen.getByRole('complementary', { name: 'Overview' }).closest('[data-sheet]')).not.toBeNull()
    expect(document.querySelector('[data-folder-footer]')).toHaveTextContent("开发助手's folder: it works here")
  })

  it('the sidebar “Project folder” opens the folder shared by the project and becomes current', async () => {
    const reads: string[] = []
    server.use(
      http.get(apiUrl(`/projects/${defaultProject.id}/context-folder/entries`), ({ request }) => {
        reads.push(new URL(request.url).searchParams.get('path') ?? '')
        return HttpResponse.json({ path: '', entries: [], nextCursor: null })
      }),
      ...baseHandlers([]),
    )
    renderRoutes(routes, `${project}/folder`)
    expect(await screen.findByRole('heading', { name: 'Project folder', level: 1 })).toBeInTheDocument()
    expect(await screen.findByRole('tree', { name: 'File tree' })).toHaveTextContent('Project folder')
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['Project folder'])
    // Redesign: the title is outside the sheet, the folder is on a sheet, and the overview is in the stub on the right.
    const sheet = screen.getByRole('complementary', { name: 'Overview' }).closest('[data-sheet]')!
    expect(sheet).not.toBeNull()
    expect(sheet).not.toContainElement(screen.getByRole('heading', { name: 'Project folder', level: 1 }))
    expect(reads).toContain('')
  })

  it('shows the empty state when there are no Agents; both the sidebar “+” and the empty-state button open the create dialog', async () => {
    server.use(...baseHandlers([]))
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentsPath)
    const empty = await screen.findByRole('region', { name: 'No Agents yet.' })
    expect(await within(sidebar()).findByText('No Agents yet.')).toBeInTheDocument()
    await user.click(within(empty).getByRole('button', { name: 'New Agent' }))
    expect(within(await screen.findByRole('dialog')).getByRole('heading', { name: 'New Agent' })).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await user.click(within(sidebar()).getByRole('button', { name: 'New Agent' }))
    expect(within(await screen.findByRole('dialog')).getByRole('heading', { name: 'New Agent' })).toBeInTheDocument()
  })

  it.each(['/skills', '/identities', '/plugins', '/settings'])('global subpage %s keeps the five global entries and the Inbox event stream', async path => {
    renderRoutes([{ path: '/', element: <AppLayout />, children: [{ path: path.slice(1), element: <p>Global page content</p> }] }], path)
    await screen.findByText('Global page content')
    for (const href of globalEntries) expect(hrefs()).toContain(href)
    expect(FakeEventSource.instances.some(source => source.url.endsWith('/inbox/events') && source.readyState !== FakeEventSource.CLOSED)).toBe(true)
  })

  it('on narrow screens the menu button opens the sidebar as a drawer, which closes by itself once an entry changes the address', async () => {
    const agent = makeAgent({ name: '开发助手' })
    server.use(...baseHandlers([agent]))
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentPath(agent.id, 'chat'))
    await within(sidebar()).findByRole('link', { name: '开发助手' })
    const menu = screen.getByRole('button', { name: 'Open navigation' })
    expect(menu).toHaveAttribute('aria-expanded', 'false')
    await user.click(menu)
    const drawer = await screen.findByRole('dialog', { name: 'Main navigation' })
    expect(menu).toHaveAttribute('aria-expanded', 'true')
    expect(await within(drawer).findByRole('link', { name: '开发助手' })).toHaveAttribute('aria-current', 'page')
    await user.click(within(drawer).getByRole('link', { name: 'Project settings' }))
    expect(router.state.location.pathname).toBe(`${project}/settings`)
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Main navigation' })).toBeNull())
    await user.click(screen.getByRole('button', { name: 'Open navigation' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Main navigation' })).getByRole('button', { name: 'Close navigation' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Main navigation' })).toBeNull())
  })

  it('⌘K focuses the sidebar search', async () => {
    server.use(...baseHandlers([makeAgent()]))
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentsPath)
    const search = await within(sidebar()).findByRole('searchbox', { name: 'Search Agents' })
    await user.keyboard('{Meta>}k{/Meta}')
    expect(search).toHaveFocus()
  })
})

describe('Agent list', () => {
  it('lists the Agents that are not deleted', async () => {
    const a = makeAgent({ name: '开发助手' })
    const b = makeAgent({ name: 'Test Assistant', model: { provider: 'openai', modelId: 'gpt-x' } })
    server.use(...baseHandlers([a, b]))
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })
    await waitFor(() => expect(within(list).getAllByRole('link').map((link) => link.textContent)).toEqual(['开发助手', 'Test Assistant']))
  })

  // jsdom does no layout, so we can only confirm that the scroll area is a positioning container; that the whole
  // page does not get stretched taller is checked with browser screenshots (.local-build/chat-scroll-fix).
  it('the scroll area of the list is a positioning container, so the description in the “needs you” badge does not stretch the whole page', async () => {
    server.use(...baseHandlers([makeAgent({ name: '开发助手' })]))
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })
    expect(list.closest('.overflow-y-auto')).toHaveClass('relative')
  })
  it('the address of a deleted Agent says it does not exist and shows the list', async () => {
    const agent = makeAgent()
    server.use(http.get(apiUrl('/agents/gone'), () => HttpResponse.json(errorBody('not_found'), { status: 404 })), ...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentPath('gone', 'chat'))
    expect(await screen.findByText('This Agent doesn\'t exist or was deleted.')).toBeInTheDocument()
    expect(await screen.findByRole('list', { name: 'Agents' })).toBeInTheDocument()
  })

  it('shows Replying and Session lost; after a refresh the Replying label goes away (idle shows no label)', async () => {
    const a = makeAgent({ name: 'A', busy: true })
    const b = makeAgent({ name: 'B' })
    let busy = true
    server.use(
      http.get(agentsUrl, () =>
        HttpResponse.json({ agents: [{ ...toListItem(a), busy }, toListItem(b, 'missing')] }),
      ),
      ...baseHandlers([a, b]),
    )
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })
    const itemA = await within(list).findByRole('link', { name: /^A/ })
    const itemB = within(list).getByRole('link', { name: /^B/ })
    expect(within(itemA).getByText('Replying')).toBeInTheDocument()
    expect(within(itemB).getByText('Session lost')).toBeInTheDocument()
    busy = false
    focusManager.setFocused(false)
    focusManager.setFocused(true)
    await waitFor(() => expect(itemA).toHaveTextContent(/^A$/))
    focusManager.setFocused(undefined)
  })

  it('refreshes the list every 5 seconds by default', () => {
    expect(config.agentListPollMs).toBe(5000)
  })
})

describe('connection and error display', () => {
  it('when the server is not running, shows the address and Retry, and the sidebar connection status is Unreachable', async () => {
    // Every endpoint is unreachable: if a default handler (e.g. pending messages)
    // responded, the notice would disappear and reappear along with it.
    server.use(http.all(apiUrl('/*'), () => HttpResponse.error()))
    renderRoutes(routes, defaultAgentsPath)
    // A request left over from the previous test case may get its response
    // later and remount the notice, so look up the notice again each time.
    await waitFor(() => {
      const alert = banner()
      expect(alert).toHaveTextContent('Can\'t reach the server')
      expect(alert).toHaveTextContent(config.apiBase)
      expect(within(alert!).getByRole('button', { name: 'Retry' })).toBeInTheDocument()
      expect(within(screen.getByRole('status', { name: 'Connection' })).getByText('Unreachable')).toBeInTheDocument()
    })
  })

  it('a disconnect after loading also shows the global notice; Retry refetches the failed queries and the notice goes away once connected', async () => {
    const agent = makeAgent({ name: '开发助手' })
    let up = true
    let listCalls = 0
    // While disconnected, every endpoint is unreachable (while connected, requests go on to the handlers below): if
    // only the list failed, other requests on the page would succeed and make the notice disappear and reappear;
    // the notice the test holds might then have been replaced, and clicking its retry would send no request.
    server.use(
      http.all(apiUrl('/*'), () => (up ? undefined : HttpResponse.error())),
      http.get(agentsUrl, () => {
        listCalls += 1
        return HttpResponse.json({ agents: [toListItem(agent)] })
      }),
      ...baseHandlers([agent]),
    )
    const user = userEvent.setup()
    const { client } = renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })
    await within(list).findByRole('link', { name: /开发助手/ })
    expect(banner()).toBeNull()

    up = false
    await act(() => client.refetchQueries({ queryKey: keys.agents }))
    const alert = await findBanner()
    expect(alert).toHaveTextContent(config.apiBase)

    up = true
    const before = listCalls
    await user.click(within(alert).getByRole('button', { name: 'Retry' }))
    await waitFor(() => expect(banner()).toBeNull())
    expect(listCalls).toBeGreaterThan(before)
    await waitFor(() => expect(screen.queryByText('Can\'t reach the server')).toBeNull())
  })

  it('a change that fails because the server is unreachable also shows the global notice, which goes away once any request succeeds', async () => {
    const agent = makeAgent()
    server.use(http.delete(apiUrl(`/agents/${agent.id}`), () => HttpResponse.error()), ...baseHandlers([agent]))
    const user = userEvent.setup()
    const { client } = renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const remove = await screen.findByRole('button', { name: 'Delete this Agent' })
    await waitFor(() => expect(remove).toBeEnabled())
    await user.click(remove)
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Delete' }))
    expect(await findBanner()).toHaveTextContent(config.apiBase)
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Cancel' }))
    await act(() => client.refetchQueries({ queryKey: keys.agents }))
    await waitFor(() => expect(banner()).toBeNull())
  })

  it('talks to the server on the local loopback address by default', () => {
    expect(config.apiBase).toBe('http://127.0.0.1:8780/api/v1')
  })

  it('a validation failure shows the reason next to the field and keeps what was entered', async () => {
    server.use(
      http.get(apiUrl('/model-providers'), () =>
        HttpResponse.json({ piVersion: '1.1.0', catalogGeneratedAt: null, providers: [{ id: 'anthropic', source: 'builtin', apiKeyEnvVars: ['ANTHROPIC_API_KEY'], modelCount: 1, authMethods: ['api_key'] }] }),
      ),
      http.get(apiUrl('/model-providers/anthropic/models'), () =>
        HttpResponse.json({ providerId: 'anthropic', models: [{ id: 'm1', name: 'M1', reasoning: false, contextWindow: 1, maxTokens: 1, input: [] }] }),
      ),
      http.post(agentsUrl, () =>
        HttpResponse.json(errorBody('validation_failed', 'bad', { fields: [{ field: 'name', message: 'Name is too long' }] }), { status: 422 }),
      ),
      ...baseHandlers([]),
    )
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentsPath)
    // When there is no Agent, the empty state in the main area also has a “New Agent”; here we use the “+” in the sidebar.
    await user.click(await within(sidebar()).findByRole('button', { name: 'New Agent' }))
    await user.type(screen.getByLabelText('Display name'), 'A very long name')
    await user.selectOptions(await screen.findByLabelText('Model provider'), 'anthropic')
    await chooseModel(user, document.body, 'm1')
    await user.click(screen.getByRole('button', { name: 'Create' }))
    expect(await screen.findByText('Name is too long')).toBeInTheDocument()
    expect(screen.getByLabelText('Display name')).toHaveValue('A very long name')
  })
})

describe('global settings page', () => {
  it('shows the default organization and project, the connection status and the CLI templates; the server information has no editing actions, and the global folder can be edited', async () => {
    server.use(
      http.get(apiUrl('/health'), () => HttpResponse.json({ ok: false, database: { ok: false, error: 'unable to open database file' } }, { status: 503 })),
      http.get(apiUrl('/cli-templates'), () =>
        HttpResponse.json({
          templates: [
            { name: 'gh', version: '2.100.0', fields: [], injection: 'env', install: { status: 'failed', error: 'checksum mismatch' }, warnings: [] },
            {
              name: 'lark-cli',
              version: '1.0.94',
              fields: [],
              injection: 'files',
              install: { status: 'installed' },
              warnings: [{ code: 'host_keychain_risk', message: 'keychain' }],
            },
          ],
        }),
      ),
      ...baseHandlers([]),
    )
    renderRoutes(routes, '/settings')
    // The names in the test data differ from the row labels, so the assertions are scoped to the value that belongs to each label.
    await waitFor(() => expect(screen.getByText('Default organization').nextElementSibling).toHaveTextContent('测试组织'))
    expect(screen.getByText('Default project').nextElementSibling).toHaveTextContent('测试项目')
    // The connection status in the sidebar also shows “Database unavailable”, so scope this to the page body.
    const page = screen.getByRole('main')
    expect(await within(page).findByText('Database unavailable')).toBeInTheDocument()
    expect(within(page).getByText('Database: unable to open database file')).toBeInTheDocument()
    const gh = await screen.findByRole('region', { name: 'gh' })
    expect(within(gh).getByText('Install failed')).toBeInTheDocument()
    expect(within(gh).getByText('checksum mismatch')).toBeInTheDocument()
    const lark = screen.getByRole('region', { name: 'lark-cli' })
    expect(within(lark).getByRole('note')).toHaveTextContent('system keychain')
    // The sidebar has the scope switcher and search, so "no editing actions"
    // (in the test title) means the parts of the page body outside the folder.
    expect(within(page).getByRole('region', { name: 'Global folder' })).toBeInTheDocument()
    const outsideFolder = (el: HTMLElement) => !el.closest('[data-file-manager]')
    // The runtime status “Check again” button only reads again; it does not modify the server.
    expect(within(page).queryAllByRole('button').filter(outsideFolder).map((button) => button.textContent)).toEqual(['Check again'])
    expect(within(page).queryAllByRole('textbox').filter(outsideFolder)).toEqual([])
  })
  it('the server section shows the version, taken from the Loop-Now-Version header of the server response', async () => {
    server.use(
      http.get(apiUrl('/health'), () =>
        HttpResponse.json({ ok: true, database: { ok: true, error: null, walBytes: { data: 0, events: 0 }, walWarning: false } }, { headers: { 'Loop-Now-Version': 'dev-d6eed7b50c04' } }),
      ),
      ...baseHandlers([]),
    )
    renderRoutes(routes, '/settings')
    await waitFor(() => expect(screen.getByText('Version', { selector: 'dt' }).nextElementSibling).toHaveTextContent('dev-d6eed7b50c04'))
  })

  it('when the database log is too large, a note shows below the connection status row, and the sidebar still shows Connected', async () => {
    server.use(
      http.get(apiUrl('/health'), () => HttpResponse.json({ ok: true, database: { ok: true, error: null, walBytes: { data: 300 << 20, events: 1 << 20 }, walWarning: true } })),
      ...baseHandlers([]),
    )
    renderRoutes(routes, '/settings')
    const page = screen.getByRole('main')
    expect(await within(page).findByText('Database: OK')).toBeInTheDocument()
    expect(within(page).getByText(/^The database log file is very large/).closest('[role="note"]')).toHaveTextContent('The service is still available and will tidy up on its own.')
    expect(within(screen.getByRole('status', { name: 'Connection' })).getByText('Connected')).toBeInTheDocument()
  })

  it('the UI language is saved in this browser, and the page reloads after switching', async () => {
    server.use(...baseHandlers([]))
    const reload = vi.spyOn(browserPage, 'reload').mockImplementation(() => {})
    const user = userEvent.setup()
    renderRoutes(routes, '/settings')
    // After the redesign, the interface language is a segmented control (it used to be a dropdown).
    const group = await screen.findByRole('radiogroup', { name: 'Language' })
    expect(within(group).getByRole('radio', { name: 'English' })).toHaveAttribute('aria-checked', 'true')
    expect(group).toHaveAccessibleDescription('Applies to this browser only. The page reloads when you switch.')
    await user.click(within(group).getByRole('radio', { name: '中文' })) // check-english: zh (the Chinese language option is named in Chinese)
    expect(window.localStorage.getItem(localeStorageKey)).toBe('zh-CN')
    expect(reload).toHaveBeenCalledOnce()
  })
})
