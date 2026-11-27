import { focusManager } from '@tanstack/react-query'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { keys } from '@/api/queries'
import { config } from '@/config'
import { localeStorageKey, page as browserPage } from '@/i18n/locale'
import { agentsUrl, apiUrl, defaultAgentPath, defaultAgentsPath, defaultProject, errorBody, makeAgent, makeProject, platformAgentsUrl, toListItem } from '@/test/api'
import { projectBackend } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, currentSidebarLinks, scopeSwitcher, sidebar, sidebarLinks } from '@/test/shell'
import { routes } from './routes'
import { chooseModel } from '@/test/model-picker'
import { openInboxStream } from '@/test/inbox'
import { AppLayout, SIDEBAR_COLLAPSED_KEY } from './AppLayout'
import { SIDEBAR_PLATFORM_OPEN_KEY } from './Sidebar'
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

const project = `/p/${encodeURIComponent('test-project')}`
const globalEntries = ['/plugins', '/skills', '/stats', '/identities', '/settings']
const hrefs = () => sidebarLinks().map((link) => link.getAttribute('href'))
const names = () => sidebarLinks().map((link) => link.textContent)

describe('sidebar and page structure', () => {
  it('the root address is the overview of the global scope: Overview is current, followed by projects, platform Agents, collapsible Platform tools (expanded by default), and Settings in the footer dock', async () => {
    window.localStorage.removeItem(SIDEBAR_PLATFORM_OPEN_KEY)
    const platform = makeAgent({ name: 'Inspector', scope: 'platform', projectId: null })
    server.use(...overviewHandlers(), http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(platform)] })), ...baseHandlers([makeAgent()]))
    const user = userEvent.setup()
    renderRoutes(routes, '/')
    expect(await screen.findByRole('heading', { name: 'Overview', level: 1 })).toBeInTheDocument()
    await within(sidebar()).findByRole('link', { name: 'Inspector' })
    expect(scopeSwitcher('Global')).toBeInTheDocument()
    // Platform group starts expanded at the bottom of the scroll area above the footer dock.
    const platformToggle = within(sidebar()).getByRole('button', { name: /Platform/ })
    expect(platformToggle).toHaveAttribute('aria-expanded', 'true')
    expect(names()).toEqual(['LOOP NOW', 'Overview', 'test-project', 'Inspector', 'Plugin market', 'Skills library', 'Stats', 'External identities', 'Global settings'])
    expect(hrefs()).toEqual(['/', '/', '/?project=test-project', `/platform/agents/${platform.id}`, ...globalEntries])
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['Overview'])
    expect(within(sidebar()).getByRole('list', { name: 'Platform Agents' })).toBeInTheDocument()
    // Collapsing the Platform group hides the four global tools and persists the disclosure state.
    await user.click(platformToggle)
    expect(platformToggle).toHaveAttribute('aria-expanded', 'false')
    expect(window.localStorage.getItem(SIDEBAR_PLATFORM_OPEN_KEY)).toBe('false')
    expect(names()).toEqual(['LOOP NOW', 'Overview', 'test-project', 'Inspector', 'Global settings'])
    expect(hrefs()).toEqual(['/', '/', '/?project=test-project', `/platform/agents/${platform.id}`, '/settings'])
    window.localStorage.removeItem(SIDEBAR_PLATFORM_OPEN_KEY)
  })

  it('the main area has a layer of paper grain and the sidebar has a right border without grain; pages use a clean full-bleed sheet', async () => {
    server.use(...overviewHandlers(), http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [] })), ...baseHandlers([makeAgent()]))
    renderRoutes(routes, '/')
    await screen.findByRole('heading', { name: 'Overview', level: 1 })
    const contentColumn = screen.getByRole('main').closest('.riso-grain')!
    expect(contentColumn).not.toBeNull()
    expect(sidebar()).toHaveClass('border-r', 'border-sidebar-border')
    expect(sidebar().closest('.riso-grain')).toBeNull()
    // Other pages using the shared PageFrame + Sheet use the same clean full-bleed sheet with no outer margin or shadow.
    const sheet = document.querySelector('[data-sheet]')!
    expect(sheet).toHaveClass('bg-surface-canvas', 'riso-grain')
    expect(sheet).not.toHaveClass('border-2', 'rounded-panel', 'shadow-print-dialog', 'mr-1.5', 'mt-2')
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
    window.localStorage.removeItem(SIDEBAR_PLATFORM_OPEN_KEY)
    const agent = makeAgent({ name: 'dev-helper' })
    server.use(...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const list = await within(sidebar()).findByRole('list', { name: 'Agents' })
    await waitFor(() => expect(within(list).getByRole('link', { name: 'dev-helper' })).toHaveAttribute('aria-current', 'page'))
    expect(scopeSwitcher('test-project')).toBeInTheDocument()
    expect(names()).toEqual(['LOOP NOW', 'Overview', 'Tasks', 'Project folder', 'Project settings', 'dev-helper', 'Plugin market', 'Skills library', 'Stats', 'External identities', 'Global settings'])
    expect(hrefs()).toEqual(['/', '/?project=test-project', `${project}/tasks`, `${project}/folder`, `${project}/settings`, `${project}/agents/${agent.id}`, ...globalEntries])
    expect(within(sidebar()).queryByRole('link', { name: /Inbox/ })).toBeNull()
    expect(await screen.findByRole('link', { name: 'Config' })).toHaveAttribute('aria-current', 'page')
    // Agent page header is one thin bar with no breadcrumb; details and back-to-project link live in the More menu.
    expect(screen.queryByRole('navigation', { name: 'Breadcrumb' })).toBeNull()
    expect(screen.getByRole('heading', { name: 'dev-helper', level: 1 })).toBeInTheDocument()
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'More' }))
    const menu = await screen.findByRole('dialog', { name: 'Agent info' })
    expect(within(menu).getByText('anthropic · claude-sonnet-5-5')).toBeInTheDocument()
    expect(within(menu).getByRole('link', { name: 'Back to test-project' })).toHaveAttribute('href', '/?project=test-project')
  })

  it('opening a global page (Plugin market, Global settings) from a project keeps the current project in the sidebar', async () => {
    window.localStorage.removeItem(SIDEBAR_PLATFORM_OPEN_KEY)
    const agent = makeAgent({ name: 'dev-helper' })
    server.use(...baseHandlers([agent]))
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentPath(agent.id, 'chat'))
    await within(sidebar()).findByRole('link', { name: 'dev-helper' })
    await user.click(within(sidebar()).getByRole('link', { name: 'Plugin market' }))
    expect(router.state.location.pathname).toBe('/plugins')
    expect(scopeSwitcher('test-project')).toBeInTheDocument()
    expect(currentSidebarLinks().map((link) => link.textContent)).toEqual(['Plugin market'])
    await user.click(within(sidebar()).getByRole('link', { name: 'Global settings' }))
    expect(router.state.location.pathname).toBe('/settings')
    expect(scopeSwitcher('test-project')).toBeInTheDocument()
    expect(within(sidebar()).getByRole('link', { name: 'dev-helper' })).not.toHaveAttribute('aria-current')
  })

  it('a platform Agent address is in the global scope: the sidebar lists the projects and platform Agents and selects it', async () => {
    const platform = makeAgent({ name: 'Inspector', scope: 'platform', projectId: null })
    server.use(http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(platform)] })), ...baseHandlers([platform]))
    renderRoutes(routes, `/platform/agents/${platform.id}/chat`)
    const list = await within(sidebar()).findByRole('list', { name: 'Platform Agents' })
    await waitFor(() => expect(within(list).getByRole('link', { name: 'Inspector' })).toHaveAttribute('aria-current', 'page'))
    expect(scopeSwitcher('Global')).toBeInTheDocument()
    expect(within(sidebar()).getByRole('list', { name: 'Projects' })).toHaveTextContent('test-project')
    expect(screen.queryByRole('navigation', { name: 'Breadcrumb' })).toBeNull()
    expect(await screen.findByRole('heading', { name: 'Inspector', level: 1 })).toBeInTheDocument()
  })

  it('the full navigation has no Inbox entry, and the Inbox event stream is still set up (messages for you and items needing you are updated through it)', async () => {
    server.use(...overviewHandlers(), ...baseHandlers([]))
    renderRoutes(routes, '/')
    await screen.findByRole('heading', { name: 'Overview', level: 1 })
    await openInboxStream(3)
    expect(within(sidebar()).queryByRole('link', { name: /Inbox/ })).toBeNull()
  })

  it('opening the address of an Agent Inbox tab directly selects the Agent and shows the Inbox tab', async () => {
    const agent = makeAgent({ name: 'dev-helper' })
    server.use(http.get(apiUrl(`/agents/${agent.id}/inbox`), () => HttpResponse.json({ messages: [], nextCursor: null })), ...baseHandlers([agent]))
    renderRoutes(routes, `${defaultAgentsPath}/${agent.id}/inbox`)
    const list = await screen.findByRole('list', { name: 'Agents' })
    await waitFor(() => expect(within(list).getByRole('link', { name: /dev-helper/ })).toHaveAttribute('aria-current', 'page'))
    const tabs = await screen.findByRole('navigation', { name: 'dev-helper' })
    expect(within(tabs).getByRole('link', { name: 'Inbox' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByText('No Inbox messages yet.')).toBeInTheDocument()
  })

  it('the Agent “Folder” tab sits between Config and Inbox and opens the one folder of this Agent', async () => {
    const agent = makeAgent({ name: 'dev-helper' })
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
    const tabs = await screen.findByRole('navigation', { name: 'dev-helper' })
    expect(within(tabs).getAllByRole('link').map((link) => link.textContent)).toEqual(['Chat', 'Config', 'Folder', 'Inbox'])
    expect(within(tabs).getByRole('link', { name: 'Folder' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByRole('tree', { name: 'File tree' })).toHaveTextContent("dev-helper's folder")
    expect(await within(screen.getByRole('region', { name: 'AGENTS.md' })).findByText('Working notes')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Pull from Git' })).toBeInTheDocument()
    expect(reads).toContain('')
    // Full-bleed 2-pane explorer on the tab's sheet, with live counts/size and the folder note in the footer strip.
    expect(document.querySelector('[data-file-manager]')?.closest('[data-sheet]')).not.toBeNull()
    const footer = document.querySelector('[data-folder-footer]')
    expect(footer).toHaveTextContent('0 folders · 1 file · 12 B')
    expect(footer).toHaveTextContent("dev-helper's folder: it works here")
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
    // The title stays in PageHeader outside the sheet; the 2-pane explorer sits on the sheet with live counts in the footer strip.
    const sheet = document.querySelector('[data-file-manager]')?.closest<HTMLElement>('[data-sheet]') ?? null
    expect(sheet).not.toBeNull()
    expect(sheet).not.toContainElement(screen.getByRole('heading', { name: 'Project folder', level: 1 }))
    await waitFor(() => expect(document.querySelector('[data-folder-footer]')).toHaveTextContent('0 folders · 0 files'))
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

  it.each(['/skills', '/identities', '/plugins', '/settings'])('global subpage %s keeps the five global entries reachable and the Inbox event stream', async path => {
    window.localStorage.removeItem(SIDEBAR_PLATFORM_OPEN_KEY)
    const user = userEvent.setup()
    renderRoutes([{ path: '/', element: <AppLayout />, children: [{ path: path.slice(1), element: <p>Global page content</p> }] }], path)
    await screen.findByText('Global page content')
    // Platform subpages auto-expand the Platform section; Settings keeps it collapsed until expanded.
    const toggle = within(sidebar()).getByRole('button', { name: /Platform/ })
    if (toggle.getAttribute('aria-expanded') === 'false') await user.click(toggle)
    for (const href of globalEntries) expect(hrefs()).toContain(href)
    expect(FakeEventSource.instances.some(source => source.url.endsWith('/inbox/events') && source.readyState !== FakeEventSource.CLOSED)).toBe(true)
    window.localStorage.removeItem(SIDEBAR_PLATFORM_OPEN_KEY)
  })

  it('on narrow screens the menu button opens the sidebar as a drawer, which closes by itself once an entry changes the address', async () => {
    const agent = makeAgent({ name: 'dev-helper' })
    server.use(...baseHandlers([agent]))
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentPath(agent.id, 'chat'))
    await within(sidebar()).findByRole('link', { name: 'dev-helper' })
    const menu = screen.getByRole('button', { name: 'Open navigation' })
    expect(menu).toHaveAttribute('aria-expanded', 'false')
    await user.click(menu)
    const drawer = await screen.findByRole('dialog', { name: 'Main navigation' })
    expect(menu).toHaveAttribute('aria-expanded', 'true')
    expect(await within(drawer).findByRole('link', { name: 'dev-helper' })).toHaveAttribute('aria-current', 'page')
    await user.click(within(drawer).getByRole('link', { name: 'Project settings' }))
    expect(router.state.location.pathname).toBe(`${project}/settings`)
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Main navigation' })).toBeNull())
    await user.click(screen.getByRole('button', { name: 'Open navigation' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Main navigation' })).getByRole('button', { name: 'Close navigation' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Main navigation' })).toBeNull())
  })

  it('⌘K opens the command palette, supports searching projects, Agents and pages with keyboard navigation, and closes on Esc', async () => {
    const other = makeProject({ name: 'design-lab' })
    const helper = makeAgent({ name: 'dev-helper' })
    const writer = makeAgent({ name: 'docs-writer' })
    server.use(...overviewHandlers(), ...projectBackend({ projects: [defaultProject, other], agents: [helper, writer] }).handlers)
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentPath(helper.id, 'chat'))
    await within(sidebar()).findByRole('link', { name: 'dev-helper' })

    // Open with ⌘K and filter to an Agent in the current scope
    await user.keyboard('{Meta>}k{/Meta}')
    const dialog = await screen.findByRole('dialog', { name: 'Command palette' })
    const input = within(dialog).getByRole('combobox')
    expect(input).toHaveFocus()

    await user.type(input, 'docs-writer')
    expect(within(dialog).getByRole('option', { name: /docs-writer/ })).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Command palette' })).toBeNull())
    expect(router.state.location.pathname).toBe(defaultAgentPath(writer.id, 'chat'))

    // Open with Ctrl+K and switch to another project via Enter (updates lastScope)
    await user.keyboard('{Control>}k{/Control}')
    const dialog2 = await screen.findByRole('dialog', { name: 'Command palette' })
    const input2 = within(dialog2).getByRole('combobox')
    await user.type(input2, 'design-lab')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(router.state.location.search).toBe(`?${new URLSearchParams({ project: 'design-lab' })}`))
    expect(router.state.location.pathname).toBe('/')
    expect(window.localStorage.getItem('loop-now.lastScope')).toBe(other.id)

    // Open via sidebar shortcut button, navigate pages with ArrowDown/ArrowUp, test empty state, and close with Esc
    await user.click(within(sidebar()).getByRole('button', { name: 'Open command palette' }))
    const dialog3 = await screen.findByRole('dialog', { name: 'Command palette' })
    const input3 = within(dialog3).getByRole('combobox')
    await user.type(input3, 'no-such-match-xyz')
    expect(within(dialog3).getByRole('status')).toHaveTextContent('No matching results')

    await user.clear(input3)
    await user.type(input3, 'plugins')
    expect(within(dialog3).getByRole('option', { name: 'Plugins' })).toBeInTheDocument()

    await user.clear(input3)
    await user.type(input3, 'settings')
    const options = within(dialog3).getAllByRole('option')
    expect(options.length).toBeGreaterThanOrEqual(2)
    expect(options[0]).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{ArrowDown}')
    expect(options[1]).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{ArrowUp}')
    expect(options[0]).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Command palette' })).toBeNull())
  })

  it('desktop sidebar collapses and expands via button and ⌘B, persists to localStorage, and ignores ⌘B while editing text', async () => {
    window.localStorage.removeItem(SIDEBAR_COLLAPSED_KEY)
    server.use(...baseHandlers([makeAgent({ name: 'dev-helper' })]))
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentsPath)
    await within(sidebar()).findByRole('link', { name: 'dev-helper' })

    // Collapse via header button
    await user.click(within(sidebar()).getByRole('button', { name: 'Collapse navigation' }))
    expect(screen.queryByRole('navigation', { name: 'Main navigation' })).toBeNull()
    expect(window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY)).toBe('true')

    // Expand via top-left expand button
    await user.click(screen.getByRole('button', { name: 'Expand navigation' }))
    expect(sidebar()).toBeInTheDocument()
    expect(window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY)).toBe('false')

    // Skip ⌘B while typing in an input
    const search = within(sidebar()).getByRole('searchbox', { name: 'Search Agents' })
    await user.click(search)
    expect(search).toHaveFocus()
    await user.keyboard('{Meta>}b{/Meta}')
    expect(sidebar()).toBeInTheDocument()
    expect(window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY)).toBe('false')

    // Toggle with ⌘B when not focused in an editable element
    search.blur()
    await user.keyboard('{Meta>}b{/Meta}')
    expect(screen.queryByRole('navigation', { name: 'Main navigation' })).toBeNull()
    expect(window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY)).toBe('true')

    // Toggle back with Ctrl+B
    await user.keyboard('{Control>}b{/Control}')
    expect(sidebar()).toBeInTheDocument()
    expect(window.localStorage.getItem(SIDEBAR_COLLAPSED_KEY)).toBe('false')
  })

  it('ScopeSwitcher selects the first matching project on Enter when filtering instead of jumping to Global', async () => {
    const writing = makeProject({ name: 'Writing team' })
    server.use(...overviewHandlers(), ...projectBackend({ projects: [defaultProject, makeProject({ name: 'model-access' }), writing] }).handlers)
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentsPath)
    await user.click(await within(sidebar()).findByRole('button', { name: 'Switch scope, current: test-project' }))
    const filter = await screen.findByRole('searchbox', { name: 'Filter projects by name' })

    // Typing a query with no matching project or Global match does not jump to Global on Enter
    await user.type(filter, 'zzz-no-match')
    await user.keyboard('{Enter}')
    expect(screen.getByRole('listbox', { name: 'Scopes' })).toBeInTheDocument()

    // Filtering to a matching project selects that project on Enter
    await user.clear(filter)
    await user.type(filter, 'Writ')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(router.state.location.search).toBe(`?${new URLSearchParams({ project: 'Writing team' })}`))
    expect(router.state.location.pathname).toBe('/')
    expect(window.localStorage.getItem('loop-now.lastScope')).toBe(writing.id)
  })

  it('project list rows, nav items, group headings and scope switcher rows stay on one line with truncation and tooltips', async () => {
    const longProject = { ...defaultProject, name: 'very-long-project-name-for-narrow-sidebar-testing' }
    const platform = makeAgent({ name: 'Inspector', scope: 'platform', projectId: null })
    server.use(
      ...overviewHandlers(),
      http.get(apiUrl('/projects'), () => HttpResponse.json({ projects: [longProject] })),
      http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(platform)] })),
      ...baseHandlers([]),
    )
    const user = userEvent.setup()
    renderRoutes(routes, '/')

    const projectsList = await within(sidebar()).findByRole('list', { name: 'Projects' })
    const projectRow = await within(projectsList).findByRole('link', { name: longProject.name })
    expect(projectRow).toHaveClass('h-8')
    expect(projectRow).not.toHaveClass('flex-wrap')
    const projectLabel = within(projectRow).getByText(longProject.name)
    expect(projectLabel).toHaveClass('min-w-0', 'flex-1', 'truncate')
    expect(projectLabel).toHaveAttribute('title', longProject.name)

    const globalEntry = within(sidebar()).getByRole('link', { name: 'External identities' })
    expect(globalEntry).toHaveClass('h-[30px]')
    expect(within(globalEntry).getByText('External identities')).toHaveClass('min-w-0', 'flex-1', 'truncate')
    expect(within(globalEntry).getByText('External identities')).toHaveAttribute('title', 'External identities')

    const platformHeading = within(sidebar()).getByRole('heading', { name: 'Platform Agents' })
    expect(platformHeading).toHaveClass('min-w-0', 'truncate')
    expect(within(sidebar()).getByRole('button', { name: 'New Agent' })).toHaveClass('shrink-0')

    const switcher = scopeSwitcher('Global')
    expect(switcher).toHaveClass('h-8')
    await user.click(switcher)
    const option = within(await screen.findByRole('listbox', { name: 'Scopes' })).getByRole('option', { name: new RegExp(longProject.name) })
    expect(option).toHaveClass('h-[38px]')
    expect(within(option).getByText(longProject.name)).toHaveClass('truncate')
    expect(within(option).getByText(longProject.name)).toHaveAttribute('title', longProject.name)
  })
})

describe('Agent list', () => {
  it('lists the Agents that are not deleted', async () => {
    const a = makeAgent({ name: 'dev-helper' })
    const b = makeAgent({ name: 'Test Assistant', model: { provider: 'openai', modelId: 'gpt-x' } })
    server.use(...baseHandlers([a, b]))
    renderRoutes(routes, defaultAgentsPath)
    const list = await screen.findByRole('list', { name: 'Agents' })
    await waitFor(() => expect(within(list).getAllByRole('link').map((link) => link.textContent)).toEqual(['dev-helper', 'Test Assistant']))
  })

  // jsdom does no layout, so we can only confirm that the scroll area is a positioning container; that the whole
  // page does not get stretched taller is checked with browser screenshots (.local-build/chat-scroll-fix).
  it('the scroll area of the list is a positioning container, so the description in the “needs you” badge does not stretch the whole page', async () => {
    server.use(...baseHandlers([makeAgent({ name: 'dev-helper' })]))
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

  it('shows Replying; after a refresh the label goes away (idle shows no label), and a lost session is no state of its own any more', async () => {
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
    // The server carries a lost session into a new one, so an older list row that still says missing shows nothing.
    expect(itemB).toHaveTextContent(/^B$/)
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
    const agent = makeAgent({ name: 'dev-helper' })
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
    await within(list).findByRole('link', { name: /dev-helper/ })
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
        HttpResponse.json({ piVersion: '1.1.0', catalogGeneratedAt: null, providers: [{ id: 'anthropic', name: null, source: 'builtin', runtime: 'pi', pluginId: null, pluginDisplayName: null, unavailableReason: null, usable: null, apiKeyEnvVars: ['ANTHROPIC_API_KEY'], modelCount: 1, authMethods: ['api_key'] }] }),
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
    await chooseModel(user, document.body, 'm1', { runtime: 'Pi', provider: 'anthropic' })
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
    const user = userEvent.setup()
    renderRoutes(routes, '/settings')
    const tabs = await screen.findByRole('navigation', { name: 'Global settings sections' })
    await user.click(within(tabs).getByRole('link', { name: 'Server & Diagnostics' }))
    // The names in the test data differ from the row labels, so the assertions are scoped to the value that belongs to each label.
    await waitFor(() => expect(screen.getByText('Default organization').nextElementSibling).toHaveTextContent('test-org'))
    expect(screen.getByText('Default project').nextElementSibling).toHaveTextContent('test-project')
    // The connection status in the sidebar also shows “Database unavailable”, so scope this to the page body.
    const page = screen.getByRole('main')
    expect(await within(page).findByText('Database unavailable')).toBeInTheDocument()
    expect(within(page).getByText('Database: unable to open database file')).toBeInTheDocument()
    const gh = await screen.findByRole('region', { name: 'gh' })
    expect(within(gh).getByText('Install failed')).toBeInTheDocument()
    expect(within(gh).getByText('checksum mismatch')).toBeInTheDocument()
    const lark = screen.getByRole('region', { name: 'lark-cli' })
    expect(within(lark).getByRole('note')).toHaveTextContent('system keychain')
    // Server & Diagnostics is read-only: no editing buttons or text inputs in the main body.
    expect(within(page).queryAllByRole('button')).toEqual([])
    expect(within(page).queryAllByRole('textbox')).toEqual([])

    // Global folder lives under Workspace & Local Mode -> Global Folder and keeps its file editing actions.
    await user.click(within(tabs).getByRole('link', { name: 'Workspace & Local Mode' }))
    const workspaceViews = await within(page).findByRole('radiogroup', { name: 'Workspace & Local Mode view' })
    await user.click(within(workspaceViews).getByRole('radio', { name: 'Global Folder' }))
    const folder = await within(page).findByRole('region', { name: 'Global folder' })
    expect(folder.querySelector('[data-file-manager]')).not.toBeNull()
    expect(within(folder).getByRole('button', { name: 'New file' })).toBeInTheDocument()
  })
  it('the server section shows the version, taken from the Loop-Now-Version header of the server response', async () => {
    server.use(
      http.get(apiUrl('/health'), () =>
        HttpResponse.json({ ok: true, database: { ok: true, error: null, walBytes: { data: 0, events: 0 }, walWarning: false } }, { headers: { 'Loop-Now-Version': 'dev-d6eed7b50c04' } }),
      ),
      ...baseHandlers([]),
    )
    const user = userEvent.setup()
    renderRoutes(routes, '/settings')
    const tabs = await screen.findByRole('navigation', { name: 'Global settings sections' })
    await user.click(within(tabs).getByRole('link', { name: 'Server & Diagnostics' }))
    await waitFor(() => expect(screen.getByText('Version', { selector: 'dt' }).nextElementSibling).toHaveTextContent('dev-d6eed7b50c04'))
  })

  it('when the database log is too large, a note shows below the connection status row, and the sidebar still shows Connected', async () => {
    server.use(
      http.get(apiUrl('/health'), () => HttpResponse.json({ ok: true, database: { ok: true, error: null, walBytes: { data: 300 << 20, events: 1 << 20 }, walWarning: true } })),
      ...baseHandlers([]),
    )
    const user = userEvent.setup()
    renderRoutes(routes, '/settings')
    const tabs = await screen.findByRole('navigation', { name: 'Global settings sections' })
    await user.click(within(tabs).getByRole('link', { name: 'Server & Diagnostics' }))
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
