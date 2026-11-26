import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { routes } from '@/app/routes'
import { Toasts } from '@/components/Toasts'
import { defaultAgentPath, defaultProject, errorBody, makeAgent } from '@/test/api'
import { fakePlugins, makeAction, makeComponents, makeDetail, makeManifest, makePlugin, makeProjectPlugin } from '@/test/plugins'
import { renderRoutes, renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { PagePluginActions } from './PluginActions'

const resendFields = {
  type: 'object',
  required: ['target'],
  properties: { target: { type: 'string', format: 'uri', title: 'Target URL' }, attempts: { type: 'integer', title: 'Attempts', minimum: 1 } },
} as unknown as Record<string, never>

// The webhook has four actions: the global “重发” (Resend, with a form), the project's “Test push”, the Agent's
// “Send it one”, and “Sync” for tasks and external identities.
const webhook = makeDetail(
  makePlugin(),
  makeManifest({
    components: makeComponents({
      actions: [
        makeAction({ id: 'resend', label: 'Resend', where: ['global'], fields: resendFields }),
        makeAction({ id: 'ping', label: 'Test push', where: ['project'] }),
        makeAction({ id: 'poke', label: 'Send it one', where: ['agent'] }),
        makeAction({ id: 'sync', label: 'Sync', where: ['task', 'identity'] }),
      ],
    }),
  }),
)

function setup(initial: Parameters<typeof fakePlugins>[0] = {}, agent = makeAgent()) {
  const fake = fakePlugins({ details: [webhook], projects: { [defaultProject.id]: { webhook: makeProjectPlugin({ enabled: true }) } }, agents: { [agent.id]: [{ pluginId: 'webhook', displayName: 'Webhook notifications', available: true, granted: true, accountIds: [], accountKinds: [] }] }, ...initial })
  server.use(...fake.handlers, ...baseHandlers([agent]))
  return { fake, agent }
}

describe('Plugin action buttons', () => {
  it('global actions sit in the plugin detail header: a form is generated from fields, and once valid, where and input are submitted; the result shows as a toast and opens the link', async () => {
    const user = userEvent.setup()
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const { fake } = setup({ actions: { 'webhook/resend': () => HttpResponse.json({ notice: 'Resent 3', url: 'https://hooks.example.com/log' }) } })
    renderRoutes(routes, '/plugins/webhook')
    const group = await screen.findByRole('group', { name: 'Actions from plugin Webhook notifications' })
    expect(within(group).getAllByRole('button').map((b) => b.textContent)).toEqual(['Resend'])
    await user.click(within(group).getByRole('button', { name: 'Resend' }))
    const dialog = await screen.findByRole('dialog', { name: 'Resend' })
    await user.click(within(dialog).getByRole('button', { name: 'Run' }))
    expect(within(dialog).getByText('Required', { selector: '.font-bold' })).toBeInTheDocument()
    await user.type(within(dialog).getByLabelText('Target URL'), 'https://hooks.example.com/x')
    await user.type(within(dialog).getByLabelText('Attempts'), '2')
    await user.click(within(dialog).getByRole('button', { name: 'Run' }))
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'POST', path: '/plugins/webhook/actions/resend', query: {}, body: { where: {}, input: { target: 'https://hooks.example.com/x', attempts: 2 } } }]))
    const toast = await screen.findByText('Resent 3')
    expect(within(toast).getByRole('link', { name: 'Open' })).toHaveAttribute('href', 'https://hooks.example.com/log')
    expect(open).toHaveBeenCalledWith('https://hooks.example.com/log', '_blank', 'noopener,noreferrer')
    expect(screen.queryByRole('dialog', { name: 'Resend' })).toBeNull()
    open.mockRestore()
  })

  it.each([
    [502, 'plugin_failed', 'The plugin hit an error'],
    [504, 'plugin_timeout', 'The plugin didn\'t respond in time'],
  ] as const)('%s %s: the dialog shows the reason and stays open', async (status, code, message) => {
    const user = userEvent.setup()
    setup({ actions: { 'webhook/resend': () => HttpResponse.json(errorBody(code, code), { status }) } })
    renderRoutes(routes, '/plugins/webhook')
    await user.click(await screen.findByRole('button', { name: 'Resend' }))
    const dialog = await screen.findByRole('dialog', { name: 'Resend' })
    await user.type(within(dialog).getByLabelText('Target URL'), 'https://hooks.example.com/x')
    await user.click(within(dialog).getByRole('button', { name: 'Run' }))
    expect(await within(dialog).findByText(message)).toBeInTheDocument()
  })

  it('422 field errors are marked on the form fields; 409 plugin_not_enabled explains it must be enabled first', async () => {
    const user = userEvent.setup()
    let calls = 0
    setup({
      actions: {
        'webhook/resend': () =>
          ++calls === 1
            ? HttpResponse.json(errorBody('validation_failed', 'invalid', { fields: [{ field: 'input.target', message: 'This address is not on the allowlist' }] }), { status: 422 })
            : HttpResponse.json(errorBody('plugin_not_enabled', 'not enabled'), { status: 409 }),
      },
    })
    renderRoutes(routes, '/plugins/webhook')
    await user.click(await screen.findByRole('button', { name: 'Resend' }))
    const dialog = await screen.findByRole('dialog', { name: 'Resend' })
    await user.type(within(dialog).getByLabelText('Target URL'), 'https://hooks.example.com/x')
    await user.click(within(dialog).getByRole('button', { name: 'Run' }))
    expect(await within(dialog).findByText('This address is not on the allowlist')).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Run' }))
    expect(await within(dialog).findByText('This plugin isn\'t enabled here yet. Enable it in the project first.')).toBeInTheDocument()
  })

  it('project actions sit in the project plugin row (once expanded) with this project as where; an action without fields only asks for confirmation', async () => {
    const user = userEvent.setup()
    const { fake } = setup()
    renderRoutes(routes, `/p/${encodeURIComponent(defaultProject.name)}/plugins`)
    const card = (await screen.findByRole('link', { name: 'Webhook notifications' })).closest('li')!
    await user.click(within(card).getByRole('button', { name: 'Configure Webhook notifications' }))
    await user.click(await within(card).findByRole('button', { name: 'Test push' }))
    const dialog = await screen.findByRole('dialog', { name: 'Test push' })
    expect(dialog).toHaveTextContent('Run "Test push"?')
    await user.click(within(dialog).getByRole('button', { name: 'Run' }))
    await waitFor(() => expect(fake.writes().at(-1)).toEqual({ method: 'POST', path: '/plugins/webhook/actions/ping', query: {}, body: { where: { projectId: defaultProject.id }, input: {} } }))
    expect(await screen.findByText('Done')).toBeInTheDocument()
  })

  it('Agent actions sit in the plugin row of the Agent config page (once expanded) with this Agent as where', async () => {
    const user = userEvent.setup()
    const { fake, agent } = setup()
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const section = await screen.findByRole('region', { name: 'Plugin grants' })
    await user.click(await within(section).findByRole('button', { name: /Webhook notifications/ }))
    await user.click(await within(section).findByRole('button', { name: 'Send it one' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Send it one' })).getByRole('button', { name: 'Run' }))
    await waitFor(() => expect(fake.writes().at(-1)?.body).toEqual({ where: { agentId: agent.id }, input: {} }))
  })

  it('task and external identity pages list the plugins with such actions; the task page lists only those the project enabled', async () => {
    const user = userEvent.setup()
    const other = makeDetail(makePlugin({ id: 'other', displayName: 'Another' }), makeManifest({ id: 'other', displayName: 'Another', components: makeComponents({ actions: [makeAction({ id: 'sync', label: 'Sync', where: ['task', 'identity'] })] }) }))
    const fake = fakePlugins({ details: [webhook, other], projects: { [defaultProject.id]: { webhook: makeProjectPlugin({ enabled: true }) } } })
    server.use(...fake.handlers)
    const task = renderWithQuery(<><PagePluginActions page="task" where={{ taskId: '73000000-0000-7000-8000-000000000001' }} projectId={defaultProject.id} /><Toasts /></>)
    await screen.findByRole('group', { name: 'Actions from plugin Webhook notifications' })
    await waitFor(() => expect(screen.queryByRole('group', { name: 'Actions from plugin Another' })).toBeNull())
    await user.click(screen.getByRole('button', { name: 'Sync' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Sync' })).getByRole('button', { name: 'Run' }))
    await waitFor(() => expect(fake.writes().at(-1)?.body).toEqual({ where: { taskId: '73000000-0000-7000-8000-000000000001' }, input: {} }))
    task.unmount()
    renderWithQuery(<PagePluginActions page="identity" where={{ identityId: '74000000-0000-7000-8000-000000000001' }} />)
    expect(await screen.findByRole('group', { name: 'Actions from plugin Another' })).toBeInTheDocument()
    expect(screen.getByRole('group', { name: 'Actions from plugin Webhook notifications' })).toBeInTheDocument()
  })
})

