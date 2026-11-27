import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { FileManager } from '@/features/folders/FileManager'
import type { FolderSource, GitPull } from '@/features/folders/folder-source'
import { apiUrl, defaultProject, errorBody, makeAgent, makeProject } from '@/test/api'
import { renderConfig } from '@/test/config'
import { inDesktopShell } from '@/test/desktop'
import { createMockFolder } from '@/test/folders'
import { serverShowsDialog } from '@/test/path-picks'
import { dir, localModeBackend } from '@/test/local-mode'
import { projectBackend } from '@/test/projects'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const limits = { maxBytes: 1 << 20, extensions: ['.md'] }
const region = (name: string) => screen.findByRole('region', { name })
const cautions = ['Changes land on the real files: what an Agent changes in these directories is written straight to your files, in tasks too, without going through a task copy.', 'This list tells Agents where to work; it doesn\'t isolate access. Agents can read and write directories that aren\'t on it just the same.']

describe('Global settings: local mode', () => {
  it('each of the four switches has a description and saves its own item when pressed; on failure the switch stays as it was and the reason is shown', async () => {
    const local = localModeBackend({ settings: { git: false } })
    server.use(...local.handlers, ...baseHandlers([]))
    const user = userEvent.setup()
    renderRoutes(routes, '/settings/workspace')
    const card = await region('Local mode')
    const directories = await within(card).findByRole('switch', { name: 'Bind local directories' })
    const git = within(card).getByRole('switch', { name: 'Use local git identity' })
    const cli = within(card).getByRole('switch', { name: 'Use local CLIs' })
    const logins = within(card).getByRole('switch', { name: 'Use local logins' })
    expect(within(card).getAllByRole('switch')).toEqual([directories, git, cli, logins])
    expect(within(card).getByText('Lets Agents use what is already on this computer. Each of the four has its own switch: the first three are on by default, and "Use local logins" stays off until you turn it on.')).toBeInTheDocument()
    expect(within(card).getByText(/"Bind local directories", "Use local CLIs" and "Use local logins" take effect when an Agent handles its next message/)).toBeInTheDocument()
    expect(directories).toBeChecked()
    expect(git).not.toBeChecked()
    expect(cli).toBeChecked()
    expect(git).toHaveAccessibleDescription(/HTTPS addresses can use the git credentials saved on this computer.*SSH keys under ~\/\.ssh work either way/)
    expect(cli).toHaveAccessibleDescription(/still sign in with the accounts configured in Loop Now.*To use those, also turn on "Use local logins"/)
    expect(logins).not.toBeChecked()
    expect(logins).toHaveAccessibleDescription(/which is the same as acting as you, and all Agents share them\. CLIs with an account configured for the Agent in Loop Now still use that account/)
    expect(directories).toHaveAccessibleDescription(/When off, the lists are kept but no Agent is told about them/)

    await user.click(git)
    await waitFor(() => expect(git).toBeChecked())
    expect(local.state.requests).toEqual([{ method: 'PATCH', path: '/api/v1/local-mode-settings', body: { git: true } }])

    local.state.failNextPut = () => HttpResponse.json(errorBody('internal_error', 'Failed to write the settings'), { status: 500 })
    await user.click(cli)
    expect(await within(card).findByRole('alert')).toBeInTheDocument()
    expect(cli).toBeChecked()
    expect(local.state.requests.at(-1)).toEqual({ method: 'PATCH', path: '/api/v1/local-mode-settings', body: { cli: false } })
  })

  it('turning on “Use local logins” asks for confirmation first, stating that it acts as you, is shared by all Agents and leaves CLIs with a configured account unchanged; cancelling saves nothing', async () => {
    const local = localModeBackend()
    server.use(...local.handlers, ...baseHandlers([]))
    const user = userEvent.setup()
    renderRoutes(routes, '/settings/workspace')
    const logins = await within(await region('Local mode')).findByRole('switch', { name: 'Use local logins' })

    await user.click(logins)
    let dialog = await screen.findByRole('alertdialog', { name: 'Turn on "Use local logins"?' })
    expect(dialog).toHaveTextContent('Agents use your logins for the tools on this computer, which is the same as acting as you.')
    expect(dialog).toHaveTextContent('All Agents share these logins, including Agents in tasks.')
    expect(dialog).toHaveTextContent('CLIs with an account configured for the Agent in Loop Now still use that account.')
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(logins).not.toBeChecked()
    expect(local.state.requests).toEqual([])

    await user.click(logins)
    dialog = await screen.findByRole('alertdialog', { name: 'Turn on "Use local logins"?' })
    await user.click(within(dialog).getByRole('button', { name: 'Turn on' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    await waitFor(() => expect(logins).toBeChecked())
    expect(local.state.requests).toEqual([{ method: 'PATCH', path: '/api/v1/local-mode-settings', body: { logins: true } }])

    // Turning it off needs no confirmation.
    await user.click(logins)
    await waitFor(() => expect(logins).not.toBeChecked())
    expect(screen.queryByRole('alertdialog')).toBeNull()
    expect(local.state.requests.at(-1)).toEqual({ method: 'PATCH', path: '/api/v1/local-mode-settings', body: { logins: false } })
  })

  it('saving fails after confirmation: the error stays in the confirmation dialog and the switch stays off', async () => {
    const local = localModeBackend()
    server.use(...local.handlers, ...baseHandlers([]))
    const user = userEvent.setup()
    renderRoutes(routes, '/settings/workspace')
    const card = await region('Local mode')
    const logins = await within(card).findByRole('switch', { name: 'Use local logins' })
    local.state.failNextPut = () => HttpResponse.json(errorBody('internal_error', 'Failed to write the settings'), { status: 500 })
    await user.click(logins)
    const dialog = await screen.findByRole('alertdialog', { name: 'Turn on "Use local logins"?' })
    await user.click(within(dialog).getByRole('button', { name: 'Turn on' }))
    expect(await within(dialog).findByRole('alert')).toBeInTheDocument()
    expect(logins).not.toBeChecked()
    // The card no longer shows the same error a second time, and none is left behind after the confirmation dialog closes.
    expect(within(card).queryByRole('alert', { hidden: true })).toBeNull()
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(within(card).queryByRole('alert')).toBeNull()
  })
})

describe('Project settings: local directories', () => {
  const project = makeProject({ name: 'model-access' })
  const path = `/p/${encodeURIComponent(project.name)}/settings/context`
  function setup(init: Parameters<typeof localModeBackend>[0] = {}) {
    const local = localModeBackend({ projects: { [project.id]: { revision: 2, directories: [dir('/Users/me/code/app', 'Main repo'), dir('/Users/me/missing-dir')] } }, ...init })
    server.use(...local.handlers, ...projectBackend({ projects: [defaultProject, project] }).handlers)
    return { local, user: userEvent.setup(), ...renderRoutes(routes, path) }
  }
  const rows = async () => within(await screen.findByRole('list', { name: 'Local directories' })).getAllByRole('listitem')

  it('lists paths and notes and marks the row whose directory doesn\'t exist; states plainly that changes land on the real files and that the list doesn\'t isolate access', async () => {
    setup()
    const card = await region('Local directories')
    const [app, missing] = await rows()
    expect(app).toHaveTextContent('/Users/me/code/app')
    expect(app).toHaveTextContent('Main repo')
    expect(within(app).queryByText('Doesn\'t exist')).toBeNull()
    expect(within(missing).getByText('Doesn\'t exist')).toBeInTheDocument()
    expect(within(missing).getByText('Agents aren\'t told about this one for now.')).toBeInTheDocument()
    for (const caution of cautions) expect(within(card).getByText(caution)).toBeInTheDocument()
    // While the switch is on, there is no hint that the list is not in effect for now.
    expect(within(card).queryByText(/is off/)).toBeNull()
  })

  it('add: saves the whole list with its revision; a new directory that doesn\'t exist is saved as usual and marked in its row', async () => {
    const { local, user } = setup()
    await user.click(await within(await region('Local directories')).findByRole('button', { name: 'Add directory' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add local directory' })
    await user.type(within(dialog).getByLabelText('Absolute path'), '  /Users/me/missing-new  ')
    await user.type(within(dialog).getByLabelText('Note (optional)'), '   ')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(local.state.requests).toEqual([{
      method: 'PUT',
      path: `/api/v1/projects/${project.id}/local-directories`,
      body: { expectedRevision: 2, directories: [{ path: '/Users/me/code/app', note: 'Main repo' }, { path: '/Users/me/missing-dir', note: null }, { path: '/Users/me/missing-new', note: null }] },
    }])
    const added = (await rows())[2]
    expect(added).toHaveTextContent('/Users/me/missing-new')
    expect(within(added).getByText('Doesn\'t exist')).toBeInTheDocument()
  })

  it('edit and remove: only that row changes; before removing, says that no files are deleted', async () => {
    const { local, user } = setup()
    await user.click(await screen.findByRole('button', { name: 'Edit /Users/me/code/app' }))
    const dialog = await screen.findByRole('dialog', { name: 'Edit local directory' })
    const save = within(dialog).getByRole('button', { name: 'Save' })
    expect(save).toBeDisabled()
    const note = within(dialog).getByLabelText('Note (optional)')
    await user.clear(note)
    await user.type(note, 'Frontend')
    await user.click(save)
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(local.state.requests.at(-1)?.body).toEqual({ expectedRevision: 2, directories: [{ path: '/Users/me/code/app', note: 'Frontend' }, { path: '/Users/me/missing-dir', note: null }] })
    expect((await rows())[0]).toHaveTextContent('Frontend')

    await user.click(screen.getByRole('button', { name: 'Remove /Users/me/missing-dir' }))
    const confirm = await screen.findByRole('alertdialog', { name: 'Remove "/Users/me/missing-dir" from the list?' })
    expect(confirm).toHaveTextContent('This only removes it from the list. The directory and its files aren\'t deleted.')
    await user.click(within(confirm).getByRole('button', { name: 'Remove' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    expect(local.state.requests.at(-1)?.body).toEqual({ expectedRevision: 3, directories: [{ path: '/Users/me/code/app', note: 'Frontend' }] })
    expect(await rows()).toHaveLength(1)
  })

  it('a field error from the server shows under the input of the changed row, and the dialog keeps the input', async () => {
    const { local, user } = setup()
    local.state.failNextPut = () =>
      HttpResponse.json(errorBody('validation_failed', 'directories[2].path: Must be an absolute path', { fields: [{ field: 'directories[2].path', message: 'Must be an absolute path' }] }), { status: 422 })
    await user.click(await screen.findByRole('button', { name: 'Add directory' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add local directory' })
    const input = within(dialog).getByLabelText('Absolute path')
    await user.type(input, 'code/app')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await within(dialog).findByText('Must be an absolute path')).toBeInTheDocument()
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(input).toHaveValue('code/app')
    // The field error is already shown under the input, so it is not repeated for the whole form.
    expect(within(dialog).queryByRole('alert')).toBeNull()
  })

  it('when the list was changed elsewhere (412), loads the latest version and says so; saving again applies the change to the new list', async () => {
    const { local, user } = setup()
    await user.click(await screen.findByRole('button', { name: 'Add directory' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add local directory' })
    await user.type(within(dialog).getByLabelText('Absolute path'), '/srv/data')
    local.state.projects.set(project.id, { revision: 3, directories: [dir('/Users/me/other')] })
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await within(dialog).findByText('The list was just changed elsewhere and the latest version has been loaded. Check it, then save again.')).toBeInTheDocument()
    // While the dialog is open the rest of the page is hidden from screen readers, so query
    // with hidden to see that the list behind it has already been replaced with the latest one.
    await waitFor(() => expect(within(screen.getByRole('list', { name: 'Local directories', hidden: true })).getAllByRole('listitem', { hidden: true }).map((row) => row.getAttribute('aria-label'))).toEqual(['/Users/me/other']))
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(local.state.requests.at(-1)?.body).toEqual({ expectedRevision: 3, directories: [{ path: '/Users/me/other', note: null }, { path: '/srv/data', note: null }] })
  })

  it('in the desktop window, “Choose…” opens the system folder dialog: the chosen path fills the input and can still be edited; cancelling keeps what is there', async () => {
    const pickPath = inDesktopShell({ answer: '/Users/me/picked' })
    const { local, user } = setup()
    await user.click(await within(await region('Local directories')).findByRole('button', { name: 'Add directory' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add local directory' })
    const input = within(dialog).getByLabelText('Absolute path')
    const choose = within(dialog).getByRole('button', { name: 'Choose…' })
    await user.click(choose)
    expect(pickPath).toHaveBeenLastCalledWith({ kind: 'directory' })
    await waitFor(() => expect(input).toHaveValue('/Users/me/picked'))
    await user.type(input, '/sub')
    pickPath.mockResolvedValueOnce(null)
    await user.click(choose)
    expect(pickPath).toHaveBeenLastCalledWith({ kind: 'directory', defaultPath: '/Users/me/picked/sub' })
    await waitFor(() => expect(choose).toBeEnabled())
    expect(input).toHaveValue('/Users/me/picked/sub')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(local.state.requests.at(-1)?.body).toEqual({ expectedRevision: 2, directories: [{ path: '/Users/me/code/app', note: 'Main repo' }, { path: '/Users/me/missing-dir', note: null }, { path: '/Users/me/picked/sub', note: null }] })
  })

  it('in a browser, “Choose…” has the server show this computer\'s folder dialog; closing the form while it is open drops the request, and with it the dialog', async () => {
    const picks = serverShowsDialog('hold')
    const { user } = setup()
    const card = await region('Local directories')
    await user.click(await within(card).findByRole('button', { name: 'Add directory' }))
    let dialog = await screen.findByRole('dialog', { name: 'Add local directory' })
    await user.click(within(dialog).getByRole('button', { name: 'Choose…' }))
    expect(within(dialog).getByRole('button', { name: 'Choosing…' })).toBeInTheDocument()
    await waitFor(() => expect(picks.requests).toEqual([{ kind: 'directory' }]))
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    await waitFor(() => expect(picks.aborted).toBe(1))

    picks.answer = { path: '/Users/me/picked' }
    await user.click(within(card).getByRole('button', { name: 'Add directory' }))
    dialog = await screen.findByRole('dialog', { name: 'Add local directory' })
    await user.click(within(dialog).getByRole('button', { name: 'Choose…' }))
    await waitFor(() => expect(within(dialog).getByLabelText('Absolute path')).toHaveValue('/Users/me/picked'))
    expect(picks.requests).toHaveLength(2)
  })

  it.each([
    ['in a browser whose server cannot show the dialog', false],
    ['in an older desktop shell without the picker', true],
  ])('%s there is no “Choose…”: the path is typed', async (_where, desktop) => {
    if (desktop) inDesktopShell({ withPicker: false })
    const { user } = setup()
    await user.click(await within(await region('Local directories')).findByRole('button', { name: 'Add directory' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add local directory' })
    expect(within(dialog).getByLabelText('Absolute path')).toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: 'Choose…' })).toBeNull()
  })

  it('with “Bind local directories” off, says that Agents aren\'t given the list for now and links to global settings', async () => {
    const { user, router } = setup({ settings: { directories: false } })
    const card = await region('Local directories')
    const note = await within(card).findByText(/"Bind local directories" is off in global settings: the list is still saved, but no Agent is told about it\./)
    expect(await rows()).toHaveLength(2)
    await user.click(within(note).getByRole('link', { name: 'Open global settings' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings/workspace'))
    expect(router.state.location.hash).toBe('#local-mode')
  })
})

describe('Agent config: local directories', () => {
  it('entries inherited from the project are read-only and show where they come from; the Agent\'s own entries can be edited, and saving replaces only its own', async () => {
    const agent = makeAgent()
    const local = localModeBackend({
      projects: { [defaultProject.id]: { revision: 1, directories: [dir('/Users/me/shared', 'Project docs')] } },
      agents: { [agent.id]: { revision: 4, directories: [dir('/Users/me/own')], projectId: defaultProject.id } },
    })
    const { user } = await renderConfig({ agent, handlers: local.handlers })
    const section = await region('Local directories')
    const inherited = await within(section).findByRole('region', { name: 'From project "test-project"' })
    expect(inherited).toHaveTextContent('/Users/me/shared')
    expect(inherited).toHaveTextContent('Project docs')
    expect(within(inherited).queryByRole('button')).toBeNull()
    expect(within(inherited).getByRole('link', { name: 'Edit in project settings' })).toHaveAttribute('href', `/p/${encodeURIComponent('test-project')}/settings/context`)
    for (const caution of cautions) expect(within(section).getByText(caution)).toBeInTheDocument()

    const own = within(section).getByRole('list', { name: 'Local directories' })
    expect(within(own).getAllByRole('listitem').map((row) => row.getAttribute('aria-label'))).toEqual(['/Users/me/own'])
    await user.click(within(section).getByRole('button', { name: 'Add directory' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add local directory' })
    await user.type(within(dialog).getByLabelText('Absolute path'), '/Users/me/notes')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(local.state.requests).toEqual([{ method: 'PUT', path: `/api/v1/agents/${agent.id}/local-directories`, body: { expectedRevision: 4, directories: [{ path: '/Users/me/own', note: null }, { path: '/Users/me/notes', note: null }] } }])
    expect(within(inherited).getAllByRole('listitem')).toHaveLength(1)
  })

  it('in the desktop window, adding and editing both offer “Choose…”; editing starts the dialog at the current path', async () => {
    const pickPath = inDesktopShell({ answer: '/Users/me/elsewhere' })
    const agent = makeAgent()
    const local = localModeBackend({
      projects: { [defaultProject.id]: { revision: 1, directories: [] } },
      agents: { [agent.id]: { revision: 4, directories: [dir('/Users/me/own')], projectId: defaultProject.id } },
    })
    const { user } = await renderConfig({ agent, handlers: local.handlers })
    const section = await region('Local directories')
    await user.click(await within(section).findByRole('button', { name: 'Add directory' }))
    const add = await screen.findByRole('dialog', { name: 'Add local directory' })
    expect(within(add).getByRole('button', { name: 'Choose…' })).toBeInTheDocument()
    await user.click(within(add).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())

    await user.click(within(section).getByRole('button', { name: 'Edit /Users/me/own' }))
    const dialog = await screen.findByRole('dialog', { name: 'Edit local directory' })
    await user.click(within(dialog).getByRole('button', { name: 'Choose…' }))
    expect(pickPath).toHaveBeenCalledWith({ kind: 'directory', defaultPath: '/Users/me/own' })
    await waitFor(() => expect(within(dialog).getByLabelText('Absolute path')).toHaveValue('/Users/me/elsewhere'))
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(local.state.requests).toEqual([{ method: 'PUT', path: `/api/v1/agents/${agent.id}/local-directories`, body: { expectedRevision: 4, directories: [{ path: '/Users/me/elsewhere', note: null }] } }])
  })

  it('a platform Agent has no inherited entries', async () => {
    const agent = makeAgent({ scope: 'platform', projectId: null, name: 'Platform assistant' })
    const local = localModeBackend({ agents: { [agent.id]: { revision: 0, directories: [], projectId: null } } })
    await renderConfig({ agent, handlers: local.handlers })
    const section = await region('Local directories')
    expect(await within(section).findByText('No local directories yet.')).toBeInTheDocument()
    expect(within(section).getByText('Tells this Agent that it can work in these local directories.')).toBeInTheDocument()
    expect(within(section).queryByRole('region')).toBeNull()
  })
})

describe('Pull from Git fails: hints about the local git identity', () => {
  const HTTPS = 'https://code.example.net/team/app.git'
  const SSH = 'git@code.example.net:team/app.git'
  const failedPull = (errorCode: GitPull['errorCode'], url: string): GitPull => ({
    id: crypto.randomUUID(), agentId: 'a', url, branch: 'main', path: 'app', status: 'failed', errorCode,
    errorMessage: 'fatal: Authentication failed', createdAt: '2026-11-10T08:00:00Z', finishedAt: '2026-11-10T08:00:05Z',
  })
  // How many times the switch is read: it only needs to be read when authentication fails for an HTTPS URL.
  function setup(errorCode: GitPull['errorCode'], url: string, git: boolean) {
    const reads = { count: 0 }
    server.use(http.get(apiUrl('/local-mode-settings'), () => {
      reads.count++
      return HttpResponse.json({ directories: true, git, cli: true, logins: false } satisfies Schemas['LocalModeSettings'])
    }))
    const base = createMockFolder({ key: 'w', preset: 'workspace', latency: 0, git: true })
    const source: FolderSource = { ...base, git: { ...base.git!, list: async () => [failedPull(errorCode, url)] } }
    const view = renderRoutes([
      { path: '/', element: <FileManager source={source} rootLabel="dev-helper's folder" limits={limits} className="h-[600px]" /> },
      { path: '/settings/workspace', element: <p>Global settings</p> },
    ], '/')
    return { reads, ...view }
  }

  it('HTTPS authentication fails with the switch off: hints to turn it on and links to local mode in global settings', async () => {
    const { router } = setup('auth_failed', HTTPS, false)
    expect(await screen.findByText('Pull failed: Authentication failed')).toBeInTheDocument()
    const hint = await screen.findByRole('note')
    expect(hint).toHaveTextContent('"Use local git identity" is off, so pulls from HTTPS addresses can\'t use the git credentials saved on this computer.')
    await userEvent.setup().click(within(hint).getByRole('link', { name: 'Turn it on in global settings' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings/workspace'))
    expect(router.state.location.hash).toBe('#local-mode')
  })

  it('HTTPS authentication fails with the switch on: no extra hint, and the git output can be viewed as usual', async () => {
    const { reads } = setup('auth_failed', HTTPS, true)
    expect(await screen.findByText('Pull failed: Authentication failed')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'View git output' })).toBeInTheDocument()
    await waitFor(() => expect(reads.count).toBe(1))
    expect(screen.queryByRole('note')).toBeNull()
  })

  it('SSH authentication fails: no hint to turn the switch on even when it is off (keys under ~/.ssh don\'t depend on it), and the git output can be viewed as usual', async () => {
    const { reads } = setup('auth_failed', SSH, false)
    expect(await screen.findByText('Pull failed: Authentication failed')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'View git output' })).toBeInTheDocument()
    expect(screen.queryByRole('note')).toBeNull()
    expect(reads.count).toBe(0)
  })

  it('SSH host not verified: whatever the switch, hints to check the fingerprint in a local terminal first', async () => {
    const { reads } = setup('host_key_unverified', SSH, false)
    expect(await screen.findByText('Pull failed: SSH host key not verified')).toBeInTheDocument()
    const hint = await screen.findByRole('note')
    expect(hint).toHaveTextContent('ssh -T git@hostname')
    expect(within(hint).queryByRole('link')).toBeNull()
    expect(reads.count).toBe(0)
  })

  it('other failures neither read the switch nor show a hint', async () => {
    const { reads } = setup('clone_failed', HTTPS, false)
    expect(await screen.findByText('Pull failed: Couldn\'t clone the repository')).toBeInTheDocument()
    expect(screen.queryByRole('note')).toBeNull()
    expect(reads.count).toBe(0)
  })
})
