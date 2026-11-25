import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject, errorBody, projectsUrl } from '@/test/api'
import { inDesktopShell } from '@/test/desktop'
import { serverShowsDialog } from '@/test/path-picks'
import { renderRoutes } from '@/test/render'
import { acpCommand, acpCommandDefinition, acpCommandStatus, acpRuntimeId, runtimeDefinitions, runtimeList } from '@/test/runtimes'
import { server } from '@/test/server'
import { RuntimesCard } from './RuntimesCard'

// Global settings, Agent runtimes: the local ACP commands people added, below the built-in runtimes.
function setup({ commands = [acpCommand()], status = acpCommandStatus() }: { commands?: Schemas['AcpCommand'][]; status?: Schemas['RuntimeStatus'] | null } = {}) {
  const sent: { method: string; path: string; body: unknown }[] = []
  let definitionReads = 0
  let list = commands
  server.use(
    http.get(apiUrl('/acp-commands'), () => HttpResponse.json({ commands: list })),
    http.get(apiUrl('/runtime-definitions'), () => {
      definitionReads += 1
      return HttpResponse.json({ runtimes: [...runtimeDefinitions().runtimes, ...list.map((command) => acpCommandDefinition(command.id, command.name))] })
    }),
    http.get(apiUrl('/runtimes'), () => HttpResponse.json({ runtimes: [...runtimeList().runtimes, ...(status ? [status] : [])] })),
    http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.post(apiUrl('/acp-commands'), async ({ request }) => {
      const body = (await request.json()) as Schemas['AcpCommandCreate']
      sent.push({ method: 'POST', path: '/acp-commands', body })
      const added = acpCommand({ id: '11111111-2222-4333-8444-555555555555', name: body.name, command: body.command, args: body.args ?? [], envNames: Object.keys(body.env ?? {}).sort() })
      list = [...list, added]
      return HttpResponse.json(added, { status: 201 })
    }),
    http.patch(apiUrl('/acp-commands/:id'), async ({ request, params }) => {
      const body = (await request.json()) as Schemas['AcpCommandUpdate']
      sent.push({ method: 'PATCH', path: `/acp-commands/${String(params.id)}`, body })
      return HttpResponse.json(list[0])
    }),
  )
  const view = renderRoutes([{ path: '*', element: <RuntimesCard /> }], '/settings')
  return { ...view, user: userEvent.setup(), sent, definitionReads: () => definitionReads }
}

const card = () => screen.findByRole('region', { name: 'Hermes' })

describe('ACP commands people added, in global settings', () => {
  it('each is listed with its command, each argument on its own, the names of its variables, and whether its program is found; not among the built-in runtimes', async () => {
    setup()
    const hermes = await card()
    expect(hermes).toHaveTextContent('HermesFound')
    expect(within(hermes).getByText('hermes').parentElement).toHaveTextContent('hermesacp--stdio')
    expect(hermes).toHaveTextContent('HERMES_API_KEYsaved')
    expect(hermes).toHaveTextContent('Path/usr/local/bin/hermes')
    expect(hermes).toHaveTextContent("The version and the login can't be seen")
    expect(screen.getAllByRole('region').map((region) => region.getAttribute('aria-label'))).toEqual(['Pi', 'Claude Code', 'Hermes'])
    expect(screen.getByText(/Pi ships with the platform/)).not.toHaveTextContent('Hermes')
  })

  it('a program not found says what to check', async () => {
    setup({ status: acpCommandStatus(undefined, { available: false, binaryPath: null, unavailableReason: 'not_found', login: null }) })
    const hermes = await card()
    expect(hermes).toHaveTextContent('Not found')
    expect(within(hermes).getByRole('note')).toHaveTextContent("hermes wasn't found on this computer: check the command is spelled right and runs in a terminal, or give its absolute path.")
  })

  it('none added yet', async () => {
    setup({ commands: [], status: null })
    expect(await screen.findByText('No commands added yet.')).toBeInTheDocument()
  })

  it('adding one warns first, checks the form, sends it, and reads the runtimes again so the picker has it', async () => {
    const { user, sent, definitionReads } = setup({ commands: [], status: null })
    await user.click(await screen.findByRole('button', { name: 'Add command' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add an ACP command' })
    expect(within(dialog).getByRole('note')).toHaveTextContent('This command runs on this computer as the user the server runs as. Only add programs you trust.')
    await user.click(within(dialog).getByRole('button', { name: 'Add' }))
    expect(within(dialog).getByText('Enter a name')).toBeInTheDocument()
    expect(within(dialog).getByText('Enter a command')).toBeInTheDocument()
    expect(sent).toEqual([])

    await user.type(within(dialog).getByLabelText('Name'), 'Hermes')
    await user.type(within(dialog).getByLabelText('Command'), '/opt/hermes/bin/hermes')
    await user.type(within(dialog).getByLabelText('Arguments'), 'acp{Enter}--model "fast one"')
    await user.click(within(dialog).getByRole('button', { name: 'Add variable' }))
    await user.type(within(dialog).getByLabelText('Variable name'), '1KEY')
    await user.type(within(dialog).getByLabelText('Value of 1KEY'), 'secret')
    await user.click(within(dialog).getByRole('button', { name: 'Add' }))
    expect(within(dialog).getByText('Use letters, digits and _ only, not starting with a digit')).toBeInTheDocument()
    await user.clear(within(dialog).getByLabelText('Variable name'))
    await user.type(within(dialog).getByLabelText('Variable name'), 'HERMES_KEY')
    const reads = definitionReads()
    await user.click(within(dialog).getByRole('button', { name: 'Add' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(sent).toEqual([{ method: 'POST', path: '/acp-commands', body: { name: 'Hermes', command: '/opt/hermes/bin/hermes', args: ['acp', '--model "fast one"'], env: { HERMES_KEY: 'secret' } } }])
    await waitFor(() => expect(definitionReads()).toBeGreaterThan(reads))
    expect(await card()).toHaveTextContent('HERMES_KEYsaved')
  })

  it('in the desktop window, the command can be chosen as a file with the system dialog, and the chosen path is what gets added', async () => {
    const pickPath = inDesktopShell({ answer: '/opt/hermes/bin/hermes' })
    const { user, sent } = setup({ commands: [], status: null })
    await user.click(await screen.findByRole('button', { name: 'Add command' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add an ACP command' })
    await user.type(within(dialog).getByLabelText('Name'), 'Hermes')
    await user.type(within(dialog).getByLabelText('Command'), 'hermes')
    await user.click(within(dialog).getByRole('button', { name: 'Choose…' }))
    // The shell starts the dialog at the value only when it is an absolute path; a bare command name opens it at its usual place.
    expect(pickPath).toHaveBeenCalledWith({ kind: 'file', defaultPath: 'hermes' })
    await waitFor(() => expect(within(dialog).getByLabelText('Command')).toHaveValue('/opt/hermes/bin/hermes'))
    await user.click(within(dialog).getByRole('button', { name: 'Add' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(sent).toEqual([{ method: 'POST', path: '/acp-commands', body: { name: 'Hermes', command: '/opt/hermes/bin/hermes', args: [], env: {} } }])
  })

  it('in a browser whose server can show this computer\'s dialog, the command can be chosen there as a file', async () => {
    const picks = serverShowsDialog({ path: '/opt/hermes/bin/hermes' })
    const { user, sent } = setup({ commands: [], status: null })
    await user.click(await screen.findByRole('button', { name: 'Add command' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add an ACP command' })
    await user.type(within(dialog).getByLabelText('Name'), 'Hermes')
    await user.click(await within(dialog).findByRole('button', { name: 'Choose…' }))
    await waitFor(() => expect(within(dialog).getByLabelText('Command')).toHaveValue('/opt/hermes/bin/hermes'))
    expect(picks.requests).toEqual([{ kind: 'file' }])
    await user.click(within(dialog).getByRole('button', { name: 'Add' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(sent).toEqual([{ method: 'POST', path: '/acp-commands', body: { name: 'Hermes', command: '/opt/hermes/bin/hermes', args: [], env: {} } }])
  })

  it('in a browser whose server cannot show the dialog, the command is typed: there is no “Choose…”', async () => {
    const { user } = setup({ commands: [], status: null })
    await user.click(await screen.findByRole('button', { name: 'Add command' }))
    const dialog = await screen.findByRole('dialog', { name: 'Add an ACP command' })
    expect(within(dialog).getByLabelText('Command')).toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: 'Choose…' })).toBeNull()
  })

  it('editing sends only what changed: a refilled variable, a removed one (null) and a new one; saved values are never shown', async () => {
    const { user, sent } = setup({ commands: [acpCommand({ envNames: ['A_KEY', 'B_KEY'] })] })
    await user.click(within(await card()).getByRole('button', { name: 'Edit Hermes' }))
    const dialog = await screen.findByRole('dialog', { name: 'Edit "Hermes"' })
    expect(within(dialog).getByRole('note')).toHaveTextContent('Only add programs you trust.')
    expect(within(dialog).getAllByText('Saved, not shown')).toHaveLength(2)
    await user.click(within(dialog).getByRole('button', { name: 'Replace A_KEY' }))
    await user.type(within(dialog).getByLabelText('Value of A_KEY'), 'new-a')
    await user.click(within(dialog).getByRole('button', { name: 'Remove B_KEY' }))
    await user.click(within(dialog).getByRole('button', { name: 'Add variable' }))
    await user.type(within(dialog).getByLabelText('Variable name'), 'C_KEY')
    await user.type(within(dialog).getByLabelText('Value of C_KEY'), 'c')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(sent).toEqual([{ method: 'PATCH', path: `/acp-commands/${acpCommand().id}`, body: { env: { A_KEY: 'new-a', C_KEY: 'c', B_KEY: null } } }])
  })

  it('saving with no change sends nothing; a field the server rejects is shown at the field', async () => {
    const { user, sent } = setup()
    await user.click(within(await card()).getByRole('button', { name: 'Edit Hermes' }))
    let dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(sent).toEqual([])

    server.use(http.patch(apiUrl('/acp-commands/:id'), () => HttpResponse.json(errorBody('validation_failed', 'invalid', { fields: [{ field: 'command', message: 'must not contain a newline' }] }), { status: 422 })))
    await user.click(within(await card()).getByRole('button', { name: 'Edit Hermes' }))
    dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('Command'), '2')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await within(dialog).findByText('must not contain a newline')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Command')).toHaveAttribute('aria-invalid', 'true')
  })

  it('deleting one that Agents still run on lists them, each linked to its config page, and keeps the dialog open', async () => {
    const platformAgent = { id: '0f3b6a5e-1c2d-4e5f-8a9b-0000000000aa', name: 'butler', projectId: null }
    const projectAgent = { id: '0f3b6a5e-1c2d-4e5f-8a9b-0000000000bb', name: 'researcher', projectId: defaultProject.id }
    let deletes = 0
    server.use(
      http.delete(apiUrl('/acp-commands/:id'), () => {
        deletes += 1
        return HttpResponse.json(errorBody('runtime_in_use', 'in use', { details: { agents: [platformAgent, projectAgent] } }), { status: 409 })
      }),
    )
    const { user } = setup()
    await user.click(within(await card()).getByRole('button', { name: 'Delete Hermes' }))
    const dialog = await screen.findByRole('alertdialog', { name: 'Delete the command "Hermes"?' })
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    const alert = await within(dialog).findByRole('alert')
    expect(alert).toHaveTextContent('2 Agents still run on Hermes, so it can\'t be deleted yet. On their config pages, move "Runs on" to another runtime first, then delete:')
    expect(within(alert).getByRole('link', { name: 'butler' })).toHaveAttribute('href', `/platform/agents/${platformAgent.id}/config`)
    expect(within(alert).getByRole('link', { name: 'researcher' })).toHaveAttribute('href', `/p/${defaultProject.name}/agents/${projectAgent.id}/config`)
    expect(alert).toHaveTextContent('butlerPlatform')
    expect(deletes).toBe(1)
    expect(screen.getByRole('alertdialog')).toBeInTheDocument()
  })

  it('deleting one no Agent runs on removes it and reads the runtimes again', async () => {
    let list = [acpCommand()]
    server.use(
      http.delete(apiUrl('/acp-commands/:id'), () => {
        list = []
        return new HttpResponse(null, { status: 204 })
      }),
    )
    const { user } = setup()
    server.use(http.get(apiUrl('/acp-commands'), () => HttpResponse.json({ commands: list })))
    await user.click(within(await card()).getByRole('button', { name: 'Delete Hermes' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Delete' }))
    expect(await screen.findByText('No commands added yet.')).toBeInTheDocument()
    expect(screen.queryByRole('region', { name: 'Hermes' })).toBeNull()
    expect(acpRuntimeId).toBe(acpCommand().runtime)
  })
})
