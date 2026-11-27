import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject, makeProject, projectsUrl } from '@/test/api'
import { FakeEventSource } from '@/test/fake-event-source'
import { anomalyPage, makeAgentOverview, makeAnomaly, makeUsageRow, nextId, renderOverview, usagePage } from '@/test/overview'
import { scopeSwitcher, sidebar } from '@/test/shell'

const other = makeProject({ name: 'model-access' })
const empty = makeProject({ name: 'Sandbox' })
const base = (name: string) => `/p/${encodeURIComponent(name)}`
const globalNavigation = () => within(sidebar()).queryAllByRole('link').filter(link => ['/plugins', '/skills', '/identities', '/settings'].includes(link.getAttribute('href') ?? ''))
const navLink = (name: string) => within(sidebar()).getByRole('link', { name })
const query = (name: string) => `/?${new URLSearchParams({ project: name })}`
const scopeToggle = () => screen.getByRole('radiogroup', { name: 'Scope' })
const overviewTab = (name: string | RegExp) => screen.getByRole('tab', { name })

// Overview event stream: when filtering by scope the URL carries query parameters, so match by "contains" rather than by suffix.
const stream = () => FakeEventSource.instances.filter((s) => s.url.includes('/overview/events')).at(-1)!
async function snapshot(agents: Schemas['AgentOverview'][]) {
  await act(async () => stream().open())
  await act(async () => {
    stream().emit('snapshot', { agents }, nextId())
    await new Promise((resolve) => setTimeout(resolve, 10))
  })
}

// Record the query parameters of the usage and anomaly requests.
function recordingHandlers(usage: Schemas['UsagePage'] = usagePage([]), anomalies: Schemas['AnomalyPage'] = anomalyPage([])) {
  const queries = { usage: [] as URLSearchParams[], anomalies: [] as URLSearchParams[] }
  return {
    queries,
    handlers: [
      http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject, other, empty] })),
      http.get(apiUrl('/projects/:projectId/agents'), () => HttpResponse.json({ agents: [] })),
      http.get(apiUrl('/usage'), ({ request }) => {
        queries.usage.push(new URL(request.url).searchParams)
        return HttpResponse.json(usage)
      }),
      http.get(apiUrl('/anomalies'), ({ request }) => {
        queries.anomalies.push(new URL(request.url).searchParams)
        return HttpResponse.json(anomalies)
      }),
    ],
  }
}

describe('Filtering the overview by project or platform', () => {
  it('one project only: all three blocks request only this project, it stays after a refresh, and rows are not labeled with their owner', async () => {
    const a = makeAgentOverview({ name: 'Planning assistant', projectId: other.id })
    const { queries, handlers } = recordingHandlers(
      usagePage([makeUsageRow({ agentName: 'Planning assistant', projectId: other.id })]),
      anomalyPage([makeAnomaly({ agentName: 'Planning assistant', projectId: other.id })]),
    )
    const { user } = await renderOverview({ path: `/?project=${encodeURIComponent('model-access')}`, handlers })
    await waitFor(() => expect(stream().url).toContain(`projectId=${other.id}`))
    await snapshot([a])
    // In project scope, Sidebar ScopeSwitcher owns scope selection and the Global All | Platform toggle is hidden.
    expect(screen.queryByRole('radiogroup', { name: 'Scope' })).toBeNull()
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('model-access')
    expect(scopeSwitcher('model-access')).toBeInTheDocument()
    expect(navLink('Overview')).toHaveAttribute('href', query(other.name))
    expect(navLink('Overview')).toHaveAttribute('aria-current', 'page')
    expect(navLink('Tasks')).toHaveAttribute('href', `${base(other.name)}/tasks`)
    expect(navLink('Project settings')).toHaveAttribute('href', `${base(other.name)}/settings`)
    // The global entries are still reachable inside a project.
    expect(globalNavigation()).toHaveLength(4)
    const table = await screen.findByRole('table', { name: /Agent/ })
    expect(within(table).getByText('Planning assistant')).toBeInTheDocument()
    expect(within(table).queryByText('model-access')).toBeNull()
    await waitFor(() => expect(queries.usage.at(-1)?.get('projectId')).toBe(other.id))
    await waitFor(() => expect(queries.anomalies.at(-1)?.get('projectId')).toBe(other.id))
    expect(queries.usage.at(-1)?.get('platform')).toBeNull()

    await user.click(overviewTab(/^Usage/))
    const usage = await screen.findByRole('region', { name: 'Usage' })
    expect(within(usage).getByText('Planning assistant')).toBeInTheDocument()
    expect(within(usage).queryByText('model-access')).toBeNull()

    await user.click(overviewTab(/^Issues 24h/))
    const anomalies = await screen.findByRole('region', { name: 'Issues and failures' })
    expect(within(anomalies).getByText('Planning assistant')).toBeInTheDocument()
    expect(within(anomalies).queryByText('model-access')).toBeNull()
  })

  it('choosing the platform: requests carry platform=true, the URL is ?platform=1, and the sidebar is in the global scope', async () => {
    const { queries, handlers } = recordingHandlers()
    const { user, router } = await renderOverview({ handlers })
    await snapshot([makeAgentOverview()])
    expect(within(scopeToggle()).getByRole('radio', { name: 'All' })).toHaveAttribute('aria-checked', 'true')
    await user.click(within(scopeToggle()).getByRole('radio', { name: 'Platform' }))
    await waitFor(() => expect(router.state.location.search).toBe('?platform=1'))
    expect(within(scopeToggle()).getByRole('radio', { name: 'Platform' })).toHaveAttribute('aria-checked', 'true')
    expect(scopeSwitcher('Global')).toBeInTheDocument()
    expect(navLink('Overview')).toHaveAttribute('href', '/')
    expect(within(sidebar()).queryByRole('link', { name: 'Tasks' })).toBeNull()
    expect(globalNavigation()).toHaveLength(4)
    await waitFor(() => expect(stream().url).toContain('platform=true'))
    await waitFor(() => expect(queries.usage.at(-1)?.get('platform')).toBe('true'))
    await waitFor(() => expect(queries.anomalies.at(-1)?.get('platform')).toBe('true'))
  })

  it('with “All” chosen, rows in run status, usage and issues are labeled with their project, the platform or a deleted project', async () => {
    const a = makeAgentOverview({ name: 'Planning assistant', projectId: other.id })
    const b = makeAgentOverview({ name: 'Integration patrol', scope: 'platform', projectId: null })
    const { handlers } = recordingHandlers(
      usagePage([
        makeUsageRow({ agentId: a.agentId, agentName: 'Planning assistant', projectId: other.id }),
        makeUsageRow({ agentName: 'Legacy integration assistant', agentDeleted: true, projectId: '00000000-0000-7000-8000-dddddddddddd' }),
      ]),
      anomalyPage([makeAnomaly({ agentName: 'Integration patrol', scope: 'platform', projectId: null })]),
    )
    const { user } = await renderOverview({ handlers })
    await snapshot([a, b])
    const table = await screen.findByRole('table', { name: /Agent/ })
    const rowOf = (name: string) => within(table).getByText(name).closest('tr')!
    await waitFor(() => expect(rowOf('Planning assistant')).toHaveTextContent('model-access'))
    expect(rowOf('Integration patrol')).toHaveTextContent('Platform')

    await user.click(overviewTab(/^Usage/))
    const usage = await screen.findByRole('region', { name: 'Usage' })
    expect(await within(usage).findByText('Deleted project')).toBeInTheDocument()
    expect(within(usage).getByText('Planning assistant').closest('li')).toHaveTextContent('model-access')

    await user.click(overviewTab(/^Issues 24h/))
    const anomalies = await screen.findByRole('region', { name: 'Issues and failures' })
    expect(within(anomalies).getByText('Integration patrol').parentElement).toHaveTextContent('Platform')
  })

  it('the chosen project has no Agents yet: says only this project is shown here and links to its Agents page', async () => {
    const { handlers } = recordingHandlers()
    await renderOverview({ path: `/?project=${encodeURIComponent('Sandbox')}`, handlers })
    await waitFor(() => expect(stream().url).toContain(`projectId=${empty.id}`))
    await snapshot([])
    expect(await screen.findByRole('heading', { name: '"Sandbox" has no Agents yet.' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Go to the Agents in "Sandbox"' })).toHaveAttribute('href', `${base('Sandbox')}/agents`)
  })

  it('the project in the URL cannot be found: says so and offers a way back to all', async () => {
    const { handlers } = recordingHandlers()
    const { user, router } = await renderOverview({ path: `/?project=${encodeURIComponent('Old name')}`, handlers })
    expect(await screen.findByRole('heading', { name: 'Can\'t find project "Old name"' })).toBeInTheDocument()
    expect(screen.queryByRole('radiogroup', { name: 'Scope' })).toBeNull()
    expect(scopeSwitcher('Old name (not found)')).toBeInTheDocument()
    expect(navLink('Tasks')).toHaveAttribute('href', `${base('Old name')}/tasks`)
    expect(within(sidebar()).queryByRole('list', { name: 'Agents' })).toBeNull()
    await user.click(screen.getByRole('button', { name: 'Back to all' }))
    await waitFor(() => expect(router.state.location.search).toBe(''))
    // The new URL reaches the page only inside a transition; wait for the page to catch up.
    await waitFor(() => expect(scopeSwitcher('Global')).toBeInTheDocument())
    expect(within(scopeToggle()).getByRole('radio', { name: 'All' })).toHaveAttribute('aria-checked', 'true')
  })

  it('after picking a project in the scope switcher, the sidebar overview link keeps that project', async () => {
    const { handlers } = recordingHandlers()
    const { user, router } = await renderOverview({ handlers })
    await snapshot([makeAgentOverview()])
    await user.click(within(sidebar()).getByRole('button', { name: /^Switch scope, current: / }))
    await user.click(within(await screen.findByRole('listbox', { name: 'Scopes' })).getByRole('option', { name: /model-access/ }))
    await waitFor(() => expect(router.state.location.search).toBe(query(other.name).slice(1)))
    expect(scopeSwitcher(other.name)).toBeInTheDocument()
    expect(screen.queryByRole('radiogroup', { name: 'Scope' })).toBeNull()
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(other.name)
    await user.click(navLink('Overview'))
    expect(router.state.location.pathname).toBe('/')
    expect(router.state.location.search).toBe(query(other.name).slice(1))
  })

  it('the logo returns to the global overview, keeps the shortcut memory, and the root page does not fill in the recent project', async () => {
    window.localStorage.setItem('loop-now.lastScope', other.id)
    const { queries, handlers } = recordingHandlers()
    const { user, router } = await renderOverview({ path: query(other.name), handlers })
    await waitFor(() => expect(queries.usage.at(-1)?.get('projectId')).toBe(other.id))
    await user.click(screen.getByRole('link', { name: 'LOOP NOW' }))
    await waitFor(() => expect(router.state.location.search).toBe(''))
    expect(router.state.location.pathname).toBe('/')
    expect(within(scopeToggle()).getByRole('radio', { name: 'All' })).toHaveAttribute('aria-checked', 'true')
    expect(scopeSwitcher('Global')).toBeInTheDocument()
    await waitFor(() => expect(queries.usage.at(-1)?.get('projectId')).toBeNull())
    expect(window.localStorage.getItem('loop-now.lastScope')).toBe(other.id)
    expect(navLink('Overview')).toHaveAttribute('href', '/')
    expect(within(sidebar()).queryByRole('link', { name: 'Tasks' })).toBeNull()
    expect(globalNavigation().map(link => link.getAttribute('href'))).toEqual(['/plugins', '/skills', '/identities', '/settings'])
    await user.click(scopeSwitcher('Global'))
    const options = within(screen.getByRole('listbox', { name: 'Scopes' })).getAllByRole('option')
    expect(options.filter(option => option.getAttribute('aria-selected') === 'true').map(option => option.textContent)).toEqual(['Global'])
  })

  it('switching between project and global via ScopeSwitcher and toggling All / Platform updates the sidebar and header controls', async () => {
    const { handlers } = recordingHandlers()
    const { user, router } = await renderOverview({ handlers })
    await snapshot([makeAgentOverview()])
    expect(scopeSwitcher('Global')).toBeInTheDocument()
    expect(within(scopeToggle()).getByRole('radio', { name: 'All' })).toHaveAttribute('aria-checked', 'true')

    // Switch to a project via the sidebar ScopeSwitcher.
    await user.click(scopeSwitcher('Global'))
    await user.click(within(await screen.findByRole('listbox', { name: 'Scopes' })).getByRole('option', { name: /model-access/ }))
    await waitFor(() => expect(scopeSwitcher('model-access')).toBeInTheDocument())
    expect(navLink('Tasks')).toHaveAttribute('href', `${base(other.name)}/tasks`)
    expect(screen.queryByRole('radiogroup', { name: 'Scope' })).toBeNull()

    // Return to Global via ScopeSwitcher, then toggle Platform and back to All.
    await user.click(scopeSwitcher('model-access'))
    await user.click(within(await screen.findByRole('listbox', { name: 'Scopes' })).getByRole('option', { name: 'Global' }))
    await waitFor(() => expect(scopeSwitcher('Global')).toBeInTheDocument())
    expect(within(sidebar()).queryByRole('link', { name: 'Tasks' })).toBeNull()
    expect(within(scopeToggle()).getByRole('radio', { name: 'All' })).toHaveAttribute('aria-checked', 'true')

    await user.click(within(scopeToggle()).getByRole('radio', { name: 'Platform' }))
    await waitFor(() => expect(router.state.location.search).toBe('?platform=1'))
    expect(scopeSwitcher('Global')).toBeInTheDocument()
    expect(within(scopeToggle()).getByRole('radio', { name: 'Platform' })).toHaveAttribute('aria-checked', 'true')

    await user.click(within(scopeToggle()).getByRole('radio', { name: 'All' }))
    await waitFor(() => expect(router.state.location.search).toBe(''))
    expect(scopeSwitcher('Global')).toBeInTheDocument()
    expect(within(scopeToggle()).getByRole('radio', { name: 'All' })).toHaveAttribute('aria-checked', 'true')
  })

  it('the platform overview is in the global scope: the sidebar lists projects and has no Tasks entry; it does not jump to the remembered project', async () => {
    window.localStorage.setItem('loop-now.lastScope', other.id)
    const { handlers } = recordingHandlers()
    await renderOverview({ path: '/?platform=1', handlers })
    expect(scopeSwitcher('Global')).toBeInTheDocument()
    expect(within(scopeToggle()).getByRole('radio', { name: 'Platform' })).toHaveAttribute('aria-checked', 'true')
    expect(await within(within(sidebar()).getByRole('list', { name: 'Projects' })).findByRole('link', { name: 'model-access' })).toHaveAttribute('href', query(other.name))
    expect(within(sidebar()).queryByRole('link', { name: 'Tasks' })).toBeNull()
    expect(window.localStorage.getItem('loop-now.lastScope')).toBe(other.id)
  })
})
