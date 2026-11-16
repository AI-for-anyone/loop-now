import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultAgentsPath, defaultProject, makeAgent, makeProject } from '@/test/api'
import { projectBackend } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { scopeSwitcher, sidebar } from '@/test/shell'

const base = (name: string) => `/p/${encodeURIComponent(name)}`
const lastScope = () => window.localStorage.getItem('loop-now.lastScope')

function setup() {
  const other = makeProject({ name: '模型接入' })
  const a = makeAgent({ name: '开发助手', projectId: other.id })
  const b = makeAgent({ name: 'Patrol assistant', scope: 'platform', projectId: null })
  const c = makeAgent({ name: 'Default assistant' })
  const backend = projectBackend({ projects: [defaultProject, other], agents: [a, b, c] })
  server.use(...backend.handlers)
  return { ...backend, other, a, b, c }
}

const agentLinks = async () => within(await screen.findByRole('list', { name: 'Agents' })).findAllByRole('link')

describe('URLs with a project', () => {
  it('opening a project Agent\'s Chat page directly: the sidebar shows that project\'s list with the Agent selected, and the scope switcher shows the project', async () => {
    const { other, a } = setup()
    renderRoutes(routes, `${base('模型接入')}/agents/${a.id}/chat`)
    const list = await screen.findByRole('list', { name: 'Agents' })
    await waitFor(() => expect(within(list).getByRole('link', { name: /开发助手/ })).toHaveAttribute('aria-current', 'page'))
    expect(within(list).queryByRole('link', { name: /Default assistant/ })).toBeNull()
    expect(screen.getByRole('link', { name: 'Chat' })).toHaveAttribute('aria-current', 'page')
    expect(scopeSwitcher('模型接入')).toBeInTheDocument()
    expect(lastScope()).toBe(other.id)
  })

  it('a project\'s Agents page does not stop at the empty hint: it opens the Manager\'s chat, or the first Agent when there is no Manager; the platform still asks to pick from the sidebar', async () => {
    const { state, other, a } = setup()
    const second = makeAgent({ name: 'Planning assistant', projectId: other.id })
    state.agents.push(second)
    state.projects.find((p) => p.id === other.id)!.managerAgentId = second.id
    const first = renderRoutes(routes, `${base('模型接入')}/agents`)
    await waitFor(() => expect(first.router.state.location.pathname).toBe(`${base('模型接入')}/agents/${second.id}/chat`))
    expect(first.router.state.historyAction).toBe('REPLACE')
    first.unmount()

    state.projects.find((p) => p.id === other.id)!.managerAgentId = null
    const next = renderRoutes(routes, `${base('模型接入')}/agents`)
    await waitFor(() => expect(next.router.state.location.pathname).toBe(`${base('模型接入')}/agents/${a.id}/chat`))
    next.unmount()

    const platform = renderRoutes(routes, '/platform/agents')
    expect(await screen.findByText('Pick an Agent in the sidebar.')).toBeInTheDocument()
    expect(platform.router.state.location.pathname).toBe('/platform/agents')
  })

  it('unknown project name: explains it may have been renamed or deleted and offers a way to the default project', async () => {
    setup()
    renderRoutes(routes, `${base('Old name')}/agents`)
    expect(await screen.findByText('Can\'t find project "Old name"')).toBeInTheDocument()
    expect(screen.getByText('It may have been renamed or deleted. Use the scope switcher at the top left to find other projects.')).toBeInTheDocument()
    expect(scopeSwitcher('Old name (not found)')).toBeInTheDocument()
    expect(within(sidebar()).getByRole('link', { name: 'Overview' })).toHaveAttribute('href', `/?${new URLSearchParams({ project: 'Old name' })}`)
    expect(within(sidebar()).queryByRole('list', { name: 'Agents' })).toBeNull()
    expect(screen.getByRole('link', { name: 'Go to the default project' })).toHaveAttribute('href', defaultAgentsPath)
  })

  it('names are case-sensitive, and project names with spaces and special characters survive the round trip', async () => {
    const weird = makeProject({ name: 'Data Lab ?%' })
    const agent = makeAgent({ name: 'Analysis assistant', projectId: weird.id })
    server.use(...projectBackend({ projects: [defaultProject, weird], agents: [agent] }).handlers)
    const { router } = renderRoutes(routes, `${base('Data Lab ?%')}/agents/${agent.id}`)
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('Data Lab ?%')}/agents/${agent.id}/chat`))
    await waitFor(() => expect(screen.getByRole('link', { name: 'Chat' })).toHaveAttribute('aria-current', 'page'))
  })

  it('project renamed while open: follows it by ID to the URL with the new name and says so, without adding a history entry', async () => {
    const { state, other, a } = setup()
    const { router, client } = renderRoutes(routes, `${base('模型接入')}/agents/${a.id}/chat`)
    await agentLinks()
    state.projects.find((p) => p.id === other.id)!.name = 'Model adapters'
    await act(() => client.invalidateQueries({ queryKey: ['projects'] }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('Model adapters')}/agents/${a.id}/chat`))
    expect(router.state.historyAction).toBe('REPLACE')
    const note = await screen.findByText('Project "模型接入" was renamed to "Model adapters" elsewhere; the address has been updated.')
    // The router's new URL reaches the page only inside a transition: if "Got it" is clicked while router.state has
    // already changed but the page still shows the old URL, the old URL still follows the project by ID and the sticky note gets
    // put back. Wait until the page has also switched to the new URL ("Chat" marked as the current page) before clicking.
    await waitFor(() => expect(screen.getByRole('link', { name: 'Chat' })).toHaveAttribute('aria-current', 'page'))
    await userEvent.setup().click(within(note.parentElement!).getByRole('button', { name: 'Got it' }))
    await waitFor(() => expect(screen.queryByText(/was renamed to/)).toBeNull())
    expect(scopeSwitcher('Model adapters')).toBeInTheDocument()
  })

  it('project deleted while open: says the project was deleted and offers a way to the default project', async () => {
    const { state, other } = setup()
    const { client } = renderRoutes(routes, `${base('模型接入')}/agents`)
    await agentLinks()
    state.projects = state.projects.filter((p) => p.id !== other.id)
    await act(() => client.invalidateQueries({ queryKey: ['projects'] }))
    expect(await screen.findByText('Project "模型接入" was deleted')).toBeInTheDocument()
    expect(screen.getByText('It was deleted elsewhere; its Agents are hidden from the list.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Go to the default project' })).toBeInTheDocument()
  })

  it('opening an old URL: switches to the URL where the Agent actually lives and keeps the hash', async () => {
    const { a } = setup()
    const { router } = renderRoutes(routes, `/agents/${a.id}/chat#run-r1`)
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('模型接入')}/agents/${a.id}/chat`))
    expect(router.state.location.hash).toBe('#run-r1')
  })

  it('the project in the URL does not match the Agent: switches to the URL where the platform Agent actually lives, and the sidebar switches to global', async () => {
    const { b } = setup()
    const { router } = renderRoutes(routes, `${defaultAgentsPath}/${b.id}/config`)
    await waitFor(() => expect(router.state.location.pathname).toBe(`/platform/agents/${b.id}/config`))
    // The default project's list also appeared on the page before the rewrite, so look up the list again each time.
    await waitFor(() =>
      expect(within(screen.getByRole('list', { name: 'Platform Agents' })).getByRole('link', { name: /Patrol assistant/ })).toHaveAttribute('aria-current', 'page'),
    )
    expect(scopeSwitcher('Global')).toBeInTheDocument()
  })

  it('old URL /agents: opens the most recently used platform; when the recorded project was deleted, opens the default project', async () => {
    setup()
    window.localStorage.setItem('loop-now.lastScope', 'platform')
    const first = renderRoutes(routes, '/agents')
    await waitFor(() => expect(first.router.state.location.pathname).toBe('/platform/agents'))
    first.unmount()

    window.localStorage.setItem('loop-now.lastScope', '00000000-0000-7000-8000-ffffffffffff')
    const second = renderRoutes(routes, '/agents')
    await waitFor(() => expect(second.router.state.location.pathname).toBe(defaultAgentsPath))
  })
})

// Switching scope enters the overview of the chosen scope, and the overview reads usage and anomalies.
const overviewHandlers = [
  http.get(apiUrl('/usage'), () => HttpResponse.json({ from: null, to: null, rows: [] })),
  http.get(apiUrl('/anomalies'), () => HttpResponse.json({ items: [], total: 0, nextCursor: null })),
]

describe('Scope switcher', () => {
  it('lists global and then each project (default first, with Agent counts); choosing a project opens its overview and remembers it, and the sidebar switches to its Agents', async () => {
    const { other } = setup()
    server.use(...overviewHandlers)
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentsPath)
    await agentLinks()
    await user.click(scopeSwitcher('测试项目'))
    const options = within(await screen.findByRole('listbox', { name: 'Scopes' })).getAllByRole('option')
    expect(options.map((o) => o.textContent)).toEqual(['Global', '测试项目Default1 Agent', '模型接入1 Agent'])
    expect(options[1]).toHaveAttribute('aria-selected', 'true')
    await user.click(options[2])
    await waitFor(() => expect(router.state.location.search).toBe(`?${new URLSearchParams({ project: '模型接入' })}`))
    expect(router.state.location.pathname).toBe('/')
    expect(scopeSwitcher('模型接入')).toBeInTheDocument()
    await waitFor(async () => expect((await agentLinks()).map((l) => l.textContent)).toEqual(['开发助手']))
    expect(lastScope()).toBe(other.id)
  })

  it('switching to global by keyboard: arrow keys move and Enter selects; global lists the projects and the platform Agents', async () => {
    setup()
    server.use(...overviewHandlers)
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentsPath)
    await agentLinks()
    await user.click(scopeSwitcher('测试项目'))
    await screen.findByRole('listbox', { name: 'Scopes' })
    await user.keyboard('{ArrowUp}{Enter}')
    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(router.state.location.search).toBe('')
    expect(scopeSwitcher('Global')).toBeInTheDocument()
    expect(within(sidebar()).getByRole('list', { name: 'Projects' })).toHaveTextContent('测试项目模型接入')
    expect(await within(within(sidebar()).getByRole('list', { name: 'Platform Agents' })).findByRole('link', { name: 'Patrol assistant' })).toBeInTheDocument()
    expect(lastScope()).toBe('platform')
  })

  it('filters projects by name; “Global” and “New project” stay available, and there is no project settings shortcut any more', async () => {
    const extra = makeProject({ name: 'Writing team' })
    server.use(...projectBackend({ projects: [defaultProject, makeProject({ name: '模型接入' }), extra] }).handlers)
    const user = userEvent.setup()
    renderRoutes(routes, defaultAgentsPath)
    await user.click(await within(sidebar()).findByRole('button', { name: 'Switch scope, current: 测试项目' }))
    await user.type(await screen.findByRole('searchbox', { name: 'Filter projects by name' }), 'Writ')
    const options = within(screen.getByRole('listbox', { name: 'Scopes' })).getAllByRole('option')
    expect(options.map((o) => o.textContent?.replace(/\d+ Agents?$/, ''))).toEqual(['Global', 'Writing team'])
    const popover = screen.getByRole('dialog', { name: 'Scopes' })
    expect(within(popover).getByRole('button', { name: 'New project' })).toBeInTheDocument()
    expect(within(popover).queryByRole('link')).toBeNull()
  })

  it('the “Projects” group in the global scope: clicking a project switches to its overview', async () => {
    const { other } = setup()
    server.use(...overviewHandlers)
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, '/')
    await user.click(await within(await within(sidebar()).findByRole('list', { name: 'Projects' })).findByRole('link', { name: '模型接入' }))
    expect(router.state.location.search).toBe(`?${new URLSearchParams({ project: '模型接入' })}`)
    expect(scopeSwitcher('模型接入')).toBeInTheDocument()
    expect(lastScope()).toBe(other.id)
  })

  it('opening a global page directly keeps the most recently used project; without a record it is global', async () => {
    const { other } = setup()
    window.localStorage.setItem('loop-now.lastScope', other.id)
    const first = renderRoutes(routes, '/skills')
    expect(await within(sidebar()).findByRole('button', { name: 'Switch scope, current: 模型接入' })).toBeInTheDocument()
    expect(await within(await screen.findByRole('list', { name: 'Agents' })).findByRole('link', { name: '开发助手' })).toBeInTheDocument()
    first.unmount()

    window.localStorage.removeItem('loop-now.lastScope')
    renderRoutes(routes, '/skills')
    expect(await within(sidebar()).findByRole('button', { name: 'Switch scope, current: Global' })).toBeInTheDocument()
  })
})

describe('New project', () => {
  it('checks the naming rules first; on a duplicate name, explains next to the name and keeps the input; on success, opens the new project\'s empty Agents page', async () => {
    const { state } = setup()
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, defaultAgentsPath)
    await user.click(await within(sidebar()).findByRole('button', { name: 'Switch scope, current: 测试项目' }))
    await user.click(await screen.findByRole('button', { name: 'New project' }))
    const dialog = await screen.findByRole('dialog', { name: 'New project' })
    const input = within(dialog).getByLabelText('Project name')

    await user.type(input, 'a/b')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(within(dialog).getByText('Names can\'t contain "/" or "#"')).toBeInTheDocument()
    expect(state.requests.filter((r) => r.method === 'POST')).toHaveLength(0)

    await user.clear(input)
    await user.type(input, '模型接入')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    expect(await within(dialog).findByText('A project with this name already exists. Pick another.')).toBeInTheDocument()
    expect(input).toHaveValue('模型接入')

    await user.clear(input)
    await user.type(input, 'Docs site')
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('Docs site')}/agents`))
    expect(await screen.findByRole('region', { name: 'No Agents yet.' })).toBeInTheDocument()
    expect(scopeSwitcher('Docs site')).toBeInTheDocument()
  })
})

describe('The Skills library is global', () => {
  const templates: Schemas['SkillTemplateSummary'][] = ['pdf-tools', 'git-review'].map((name) => ({
    id: `sk-${name}`,
    name,
    description: `Description of ${name}`,
    fileCount: 1,
    createdAt: '2026-10-12T08:00:00Z',
    updatedAt: '2026-10-12T08:00:00Z',
  }))

  it('the Skills library page opened from a project and “Add from library” for Agents in different scopes list the same templates', async () => {
    const { other, a, b } = setup()
    server.use(http.get(apiUrl('/skills'), () => HttpResponse.json({ skills: templates })))
    const user = userEvent.setup()
    window.localStorage.setItem('loop-now.lastScope', other.id)
    const { unmount } = renderRoutes(routes, '/skills')
    const names = async () => (await screen.findAllByRole('link', { name: /pdf-tools|git-review/ })).map((l) => l.textContent)
    const first = await names()
    expect(first).toHaveLength(2)
    unmount()

    for (const path of [`${base('模型接入')}/agents/${a.id}/config`, `/platform/agents/${b.id}/config`]) {
      const view = renderRoutes(routes, path)
      await user.click(within(await screen.findByRole('region', { name: 'Skills' })).getByRole('button', { name: 'Add from library' }))
      const dialog = await screen.findByRole('dialog')
      expect(await within(dialog).findByText('pdf-tools')).toBeInTheDocument()
      expect(within(dialog).getByText('git-review')).toBeInTheDocument()
      view.unmount()
    }
  })
})
