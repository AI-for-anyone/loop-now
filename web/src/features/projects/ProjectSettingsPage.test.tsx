import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { defaultAgentsPath, defaultProject, makeAgent, makeProject } from '@/test/api'
import { chooseModel } from '@/test/model-picker'
import { projectBackend } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { findScopeSwitcher, sidebar } from '@/test/shell'

const base = (name: string) => `/p/${encodeURIComponent(name)}`

function setup(options: { agents?: 'some' | 'none' } = {}) {
  const other = makeProject({ name: 'model-access' })
  const dev = makeAgent({ name: 'dev-helper', projectId: other.id })
  const planner = makeAgent({ name: 'Planning assistant', projectId: other.id })
  const backend = projectBackend({ projects: [defaultProject, other], agents: options.agents === 'none' ? [] : [dev, planner] })
  server.use(...backend.handlers)
  return { ...backend, other, dev, planner }
}

const card = (name: string) => screen.findByRole('region', { name })

describe('Renaming a project', () => {
  it('after a successful rename the URL switches to the new name, and the scope switcher shows the new name', async () => {
    const { state, other } = setup()
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, `${base('model-access')}/settings`)
    const nameCard = await card('Project name')
    expect(within(nameCard).getByText(other.id)).toBeInTheDocument()
    const input = within(nameCard).getByLabelText('Project name')
    await user.clear(input)
    await user.type(input, '模型适配') // check-english: zh (decoded display of a non-ASCII project name in the URL)
    await user.click(within(nameCard).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('模型适配')}/settings`)) // check-english: zh (decoded display of a non-ASCII project name in the URL)
    expect(state.requests).toContainEqual({ method: 'PATCH', path: `/api/v1/projects/${other.id}`, body: { name: '模型适配' } }) // check-english: zh (decoded display of a non-ASCII project name in the URL)
    expect(await findScopeSwitcher('模型适配')).toBeInTheDocument() // check-english: zh (decoded display of a non-ASCII project name in the URL)
    const saved = await within(await card('Project name')).findByRole('status')
    expect(saved).toHaveTextContent('Saved')
    expect(saved).toHaveTextContent('Address changed to /p/模型适配/settings') // check-english: zh (decoded display of a non-ASCII project name in the URL)
    expect(within(await card('Project name')).getByLabelText('Project name')).toHaveValue('模型适配') // check-english: zh (decoded display of a non-ASCII project name in the URL)
    // Renamed in this console, so the “renamed elsewhere” sticky note is not shown.
    expect(screen.queryByText(/was renamed to/)).toBeNull()
  })

  it('on a duplicate name, explains next to the name and leaves the project unchanged', async () => {
    setup()
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, `${base('model-access')}/settings`)
    const nameCard = await card('Project name')
    const input = within(nameCard).getByLabelText('Project name')
    await user.clear(input)
    await user.type(input, 'test-project')
    await user.click(within(nameCard).getByRole('button', { name: 'Save' }))
    expect(await within(nameCard).findByText('A project with this name already exists. Pick another.')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe(`${base('model-access')}/settings`)
  })

  it('the default project can be renamed but not deleted', async () => {
    setup()
    const user = userEvent.setup()
    renderRoutes(routes, `${base('test-project')}/settings`)
    expect(within(await card('Project name')).getByRole('textbox', { name: 'Project name' })).toBeEnabled()
    await user.click(screen.getByRole('link', { name: 'Danger' }))
    const danger = await card('Delete project')
    expect(within(danger).getByText('The default project can\'t be deleted, but it can be renamed.')).toBeInTheDocument()
    expect(within(danger).queryByRole('button')).toBeNull()
  })
})

describe('Project Manager', () => {
  it('assigns, changes and removes the Manager; the list and the details mark the Manager', async () => {
    const { state, other, dev, planner } = setup()
    const user = userEvent.setup()
    renderRoutes(routes, `${base('model-access')}/settings`)
    const managerCard = await card('Manager')
    expect(within(managerCard).getByText('This project has no Manager yet')).toBeInTheDocument()

    await user.click(await within(managerCard).findByRole('combobox', { name: 'Assign Manager' }))
    await user.click(await screen.findByRole('option', { name: 'dev-helper' }))
    expect(await within(managerCard).findByText('"dev-helper" is now the Manager')).toBeInTheDocument()
    expect(within(managerCard).getByRole('link', { name: 'dev-helper' })).toHaveAttribute('href', `${base('model-access')}/agents/${dev.id}/chat`)
    expect(state.requests).toContainEqual({ method: 'PUT', path: `/api/v1/projects/${other.id}/manager`, body: { agentId: dev.id } })

    await user.click(within(managerCard).getByRole('combobox', { name: 'Change Manager' }))
    await user.click(await screen.findByRole('option', { name: 'Planning assistant' }))
    expect(await within(managerCard).findByText('"Planning assistant" is now the Manager')).toBeInTheDocument()
    expect(state.projects.find((p) => p.id === other.id)?.managerAgentId).toBe(planner.id)

    await user.click(within(managerCard).getByRole('button', { name: 'Remove Manager' }))
    expect(await within(managerCard).findByText('Manager removed')).toBeInTheDocument()
    expect(within(managerCard).getByText('This project has no Manager yet')).toBeInTheDocument()
    expect(state.requests.at(-1)).toMatchObject({ method: 'DELETE', path: `/api/v1/projects/${other.id}/manager` })
  })

  it('the Agent list and the details header mark the Manager', async () => {
    const { state, other, dev } = setup()
    state.projects.find((p) => p.id === other.id)!.managerAgentId = dev.id
    renderRoutes(routes, `${base('model-access')}/agents/${dev.id}/config`)
    const list = await screen.findByRole('list', { name: 'Agents' })
    await waitFor(() => expect(within(list).getByRole('link', { name: /dev-helper/ })).toHaveTextContent('Manager'))
    expect(within(list).getByRole('link', { name: /Planning assistant/ })).not.toHaveTextContent('Manager')
    const heading = await screen.findByRole('heading', { level: 1, name: 'dev-helper' })
    expect(heading.parentElement).toHaveTextContent('Manager')
  })

  it('when the project has no Agents, explains that an Agent has to be created first', async () => {
    setup({ agents: 'none' })
    renderRoutes(routes, `${base('model-access')}/settings`)
    const managerCard = await card('Manager')
    expect(await within(managerCard).findByText('The project has no Agents yet. Create one first.')).toBeInTheDocument()
    await userEvent.setup().click(within(managerCard).getByRole('button', { name: 'Create an Agent' }))
    expect(await screen.findByRole('dialog', { name: 'New Agent' })).toBeInTheDocument()
  })
})

describe('Delete project', () => {
  it('the confirmation states the Agent count; while some Agents are not idle it lists them; once all are idle it deletes and opens the default project', async () => {
    const { state, other, dev } = setup()
    state.busy.add(dev.id)
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, `${base('model-access')}/settings/danger`)
    const danger = await card('Delete project')
    await user.click(within(danger).getByRole('button', { name: 'Delete this project' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Delete project "model-access"?' })
    expect(confirm).toHaveTextContent('This also deletes the project\'s 2 Agents and its shared accounts and credentials')
    await user.click(within(confirm).getByRole('button', { name: 'Delete project' }))

    const alert = await within(danger).findByRole('alert')
    expect(alert).toHaveTextContent('Some Agents in the project aren\'t idle, so the project wasn\'t deleted.')
    expect(within(alert).getByRole('link', { name: 'Open chat with "dev-helper"' })).toHaveAttribute('href', `${base('model-access')}/agents/${dev.id}/chat`)
    expect(router.state.location.pathname).toBe(`${base('model-access')}/settings/danger`)

    state.busy.clear()
    await user.click(within(danger).getByRole('button', { name: 'Delete this project' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Delete project' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(defaultAgentsPath))
    expect(state.projects.map((p) => p.id)).not.toContain(other.id)
    await user.click(await findScopeSwitcher('test-project'))
    await waitFor(() =>
      expect(within(screen.getByRole('listbox', { name: 'Scopes' })).queryByRole('option', { name: /model-access/ })).toBeNull(),
    )
  })
})

describe('Creating and deleting Agents by scope', () => {
  it('creating an Agent on the platform: it is created on the platform and appears in the platform list', async () => {
    const { state } = setup()
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, '/platform/agents')
    await user.click(await within(sidebar()).findByRole('button', { name: 'New Agent' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('Display name'), 'Patrol assistant')
    await chooseModel(user, dialog, 'claude-sonnet-5-5', { runtime: 'Pi', provider: 'anthropic' })
    await user.click(within(dialog).getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(router.state.location.pathname).toMatch(/^\/platform\/agents\/.+\/config$/))
    expect(state.requests).toContainEqual(expect.objectContaining({ method: 'POST', path: '/api/v1/platform/agents' }))
    expect(state.agents.find((a) => a.name === 'Patrol assistant')).toMatchObject({ scope: 'platform', projectId: null })
    await waitFor(() => expect(within(screen.getByRole('list', { name: 'Platform Agents' })).getByRole('link', { name: /Patrol assistant/ })).toBeInTheDocument())
  })

  it('deleting the Agent that is the Manager: the confirmation says the project will lose its Manager, and after deleting, opens the remaining Agent of the project', async () => {
    const { state, other, dev, planner } = setup()
    state.projects.find((p) => p.id === other.id)!.managerAgentId = dev.id
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, `${base('model-access')}/agents/${dev.id}/config`)
    await user.click(await screen.findByRole('button', { name: 'Delete this Agent' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent('It\'s the project Manager, so the project will no longer have a Manager.')
    await user.click(within(confirm).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('model-access')}/agents/${planner.id}/chat`))
    expect(state.projects.find((p) => p.id === other.id)?.managerAgentId).toBeNull()
  })
})

// Tabbed project settings layout: URL-backed SubNav replacing the legacy 8-section scroll wall and 196px SectionToc stub.
describe('Project settings tabs and routing', () => {
  const region = (name: string) => screen.getByRole('region', { name })

  it('the header is the page title: the breadcrumb is the project name, the title is “Project settings”, and next to it sit the default project badge and the monospace project ID', async () => {
    setup()
    renderRoutes(routes, `${base(defaultProject.name)}/settings`)
    const title = await screen.findByRole('heading', { level: 1, name: 'Project settings' })
    const header = title.closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(`${defaultProject.name}›Project settings`)
    const meta = header.querySelector<HTMLElement>('[data-page-meta]')!
    expect(within(meta).getByText('Default project')).toHaveClass('bg-cta-bg', 'text-cta-fg', 'rounded-full')
    expect(within(meta).getByText(defaultProject.id)).toHaveClass('font-mono')
  })

  it('renders 5 URL-backed SubNav tabs without the legacy SectionToc stub and navigates cleanly across tabs', async () => {
    setup()
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, `${base('model-access')}/settings`)
    await screen.findByRole('region', { name: 'Project name' })
    expect(screen.queryByRole('navigation', { name: 'Contents' })).toBeNull()

    const nav = screen.getByRole('navigation', { name: 'Project settings sections' })
    expect(within(nav).getAllByRole('link').map((link) => link.textContent)).toEqual([
      'General',
      'Prompt & Context',
      'Credentials',
      'Plugins',
      'Danger',
    ])

    // General tab shows Project name + Manager only.
    const sheet = region('Project name').closest('[data-sheet]')!
    expect(sheet).toContainElement(region('Project name'))
    expect(sheet).toContainElement(region('Manager'))
    expect(screen.queryByRole('region', { name: 'Delete project' })).toBeNull()

    // Prompt & Context tab shows Project prompt, a compact Project folder link card (no embedded FileManager), and Local directories.
    await user.click(within(nav).getByRole('link', { name: 'Prompt & Context' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('model-access')}/settings/context`))
    const folder = await screen.findByRole('region', { name: 'Project folder' })
    expect(folder.querySelector('[data-file-manager]')).toBeNull()
    expect(within(folder).getByRole('link', { name: 'Open project folder' })).toHaveAttribute('href', `${base('model-access')}/folder`)
    expect(screen.getByRole('region', { name: 'Project prompt' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: 'Local directories' })).toBeInTheDocument()

    // Plugins tab embeds ProjectPluginsPanel directly.
    await user.click(within(nav).getByRole('link', { name: 'Plugins' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('model-access')}/settings/plugins`))
    expect(await screen.findByText(/No plugins installed yet|No plugins available/i)).toBeInTheDocument()

    // Danger tab keeps red danger styling for non-default projects.
    await user.click(within(nav).getByRole('link', { name: 'Danger' }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`${base('model-access')}/settings/danger`))
    const danger = await screen.findByRole('region', { name: 'Delete project' })
    expect(within(danger).getByRole('heading', { level: 2, name: 'Delete project' })).toHaveClass('text-red-text')
    expect(danger.querySelector('[data-section-body]')).toHaveClass('riso-stripes-red', 'bg-error-field')
  })

  it('forwards legacy #settings-* hashes and credential recovery query params to the owning tab', async () => {
    const { other } = setup()
    const hashCase = renderRoutes(routes, `${base('model-access')}/settings#settings-local-dirs`)
    await waitFor(() => expect(hashCase.router.state.location.pathname).toBe(`${base('model-access')}/settings/context`))
    expect(hashCase.router.state.location.hash).toBe('#settings-local-dirs')
    hashCase.unmount()

    const queryCase = renderRoutes(routes, `${base('model-access')}/settings?cliImport=30000000-0000-7000-8000-000000000001`)
    await waitFor(() => expect(queryCase.router.state.location.pathname).toBe(`${base('model-access')}/settings/credentials`))
    expect(queryCase.router.state.location.search).toBe('?cliImport=30000000-0000-7000-8000-000000000001')
    queryCase.unmount()

    const ghCase = renderRoutes(routes, `${base('model-access')}/settings?cliAuth.${other.id}.gh.new=30000000-0000-7000-8000-000000000002`)
    await waitFor(() => expect(ghCase.router.state.location.pathname).toBe(`${base('model-access')}/settings/credentials`))
  })

  it('the default project\'s delete section is a single note, not red', async () => {
    setup()
    renderRoutes(routes, `${base(defaultProject.name)}/settings/danger`)
    const danger = await screen.findByRole('region', { name: 'Delete project' })
    expect(within(danger).getByRole('heading', { level: 2, name: 'Delete project' })).not.toHaveClass('text-red-text')
    expect(danger.querySelector('[data-section-body]')).not.toHaveClass('riso-stripes-red')
  })
})
