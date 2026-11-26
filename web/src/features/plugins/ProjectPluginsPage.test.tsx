import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { defaultProject } from '@/test/api'
import { fakePlugins, larkAccountKind, makeAccount, makeComponents, makeConfig, makeDelivery, makeDetail, makeManifest, makePlugin, makeProjectPlugin, makeResolvedSource } from '@/test/plugins'
import { renderRoutes, renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { ProjectPluginsPanel } from './ProjectPluginsPage'

const path = `/p/${encodeURIComponent(defaultProject.name)}/plugins`
const lark = makeDetail(
  makePlugin({ id: 'lark-notify', displayName: 'Lark notifications', version: '0.3.1', source: makeResolvedSource({ kind: 'git', location: 'https://github.com/foo/lark' }) }),
  makeManifest({ id: 'lark-notify', displayName: 'Lark notifications', version: '0.3.1', configSchema: {} as never, accounts: [larkAccountKind] }),
)
const browser = makeDetail(
  makePlugin({ id: 'playwright', displayName: 'Dedicated browser', version: '2.0.0', hasPlatform: false }),
  makeManifest({ id: 'playwright', displayName: 'Dedicated browser', version: '2.0.0', events: [], permissions: [], configSchema: {} as never, components: makeComponents({ platform: false, agentExtensions: 1 }) }),
)

function renderProject() {
  const fake = fakePlugins({
    details: [makeDetail(), lark, browser],
    configs: { webhook: makeConfig({ values: { url: 'https://hooks.example.com/loop-now' } as never, revision: 2 }) },
    projects: {
      [defaultProject.id]: {
        webhook: makeProjectPlugin({ enabled: true, config: makeConfig({ values: {} as never, revision: 1 }) }),
        'lark-notify': makeProjectPlugin({ pluginId: 'lark-notify', displayName: 'Lark notifications', version: '0.3.1', enabled: true, projectFields: [] }),
      },
    },
    accounts: [makeAccount({ projectId: defaultProject.id }), makeAccount({ id: '72000000-0000-7000-8000-000000000002', projectId: defaultProject.id, name: 'Personal test', values: { appId: 'cli_9f8e7d6c5b4a' } as never })],
    deliveries: { webhook: [makeDelivery({ eventId: 'mine', projectId: defaultProject.id }), makeDelivery({ eventId: 'theirs', projectId: '00000000-0000-7000-8000-00000000ffff', eventType: 'run.ended' })] },
  })
  server.use(...fake.handlers, ...baseHandlers())
  return { ...renderRoutes(routes, path), fake }
}

const rowOf = async (name: string) => (await screen.findByRole('link', { name })).closest('li')!

describe('Project plugins page', () => {
  it('wraps in PageHeader + Sheet + PageBody, renders collapsed accordion rows with zero N+1 detail/deliveries fetches, and lazy-loads details when expanded', async () => {
    const user = userEvent.setup()
    const { fake } = renderProject()
    const title = await screen.findByRole('heading', { level: 1, name: 'Plugins' })
    const header = title.closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(`${defaultProject.name}›Project settings›Plugins`)
    expect(within(header).getByRole('link', { name: 'Plugin market' })).toHaveAttribute('href', '/plugins')

    const webhook = await rowOf('Webhook notifications')
    expect(webhook.closest('[data-sheet]')).not.toBeNull()
    expect(webhook.closest('[data-page-body]')).not.toBeNull()
    expect(within(webhook).getByRole('switch', { name: 'Enable Webhook notifications in this project' })).toHaveAttribute('aria-checked', 'true')
    const playwright = await rowOf('Dedicated browser')
    expect(within(playwright).getByRole('switch', { name: 'Enable Dedicated browser in this project' })).toHaveAttribute('aria-checked', 'false')

    // Zero N+1 detail / config / deliveries fetches while all rows stay collapsed.
    expect(fake.requests.filter((r) => r.path.startsWith('/plugins/'))).toEqual([])

    await user.click(within(playwright).getByRole('button', { name: 'Configure Dedicated browser' }))
    await waitFor(() => expect(playwright).toHaveTextContent('Agent side only. After enabling, grant it in an Agent\'s config.'))
  })

  it('enabling sends an empty project config and revision 0', async () => {
    const user = userEvent.setup()
    const { fake } = renderProject()
    const playwright = await rowOf('Dedicated browser')
    await user.click(within(playwright).getByRole('switch', { name: 'Enable Dedicated browser in this project' }))
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'PUT', path: `/projects/${defaultProject.id}/plugins/playwright`, query: {}, body: { enabled: true, values: {}, expectedRevision: 0 } }]))
    await waitFor(() => expect(within(playwright).getByRole('switch', { name: 'Enable Dedicated browser in this project' })).toHaveAttribute('aria-checked', 'true'))
  })

  it('project config has only x-scope: project fields and shows the global value once expanded; saving keeps it enabled and sends the revision', async () => {
    const user = userEvent.setup()
    const { fake } = renderProject()
    const webhook = await rowOf('Webhook notifications')
    await user.click(within(webhook).getByRole('button', { name: 'Configure Webhook notifications' }))
    const url = await within(webhook).findByLabelText('Callback URL')
    expect(within(webhook).queryByLabelText('Retry limit')).toBeNull()
    expect(webhook).toHaveTextContent('Global value: https://hooks.example.com/loop-now')
    await user.type(url, 'https://hooks.example.com/propanda')
    await user.click(within(webhook).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(fake.writes()).toEqual([{ method: 'PUT', path: `/projects/${defaultProject.id}/plugins/webhook`, query: {}, body: { enabled: true, values: { url: 'https://hooks.example.com/propanda' }, expectedRevision: 1 } }]),
    )
  })

  it('lists only the events of this project once expanded', async () => {
    const user = userEvent.setup()
    const { fake } = renderProject()
    const webhook = await rowOf('Webhook notifications')
    await user.click(within(webhook).getByRole('button', { name: 'Configure Webhook notifications' }))
    await waitFor(() => expect(within(webhook).getAllByText('inbox.received')).toHaveLength(1))
    expect(within(webhook).queryByText('run.ended')).toBeNull()
    expect(fake.requests.find((r) => r.path === '/plugins/webhook/deliveries')?.query).toEqual({ limit: '3', projectId: defaultProject.id })
  })

  it('accounts: lists the name and the start of the App ID once expanded; secrets are write-only when adding; edits send the revision; deleting asks first', async () => {
    const user = userEvent.setup()
    const { fake } = renderProject()
    const larkCard = await rowOf('Lark notifications')
    await user.click(within(larkCard).getByRole('button', { name: 'Configure Lark notifications' }))
    const accounts = await within(larkCard).findByRole('region', { name: 'Accounts · Lark app' })
    await waitFor(() => expect(within(accounts).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['Dev group botcli_a1b2EditDelete', 'Personal testcli_9f8e7d…EditDelete']))

    await user.click(within(accounts).getByRole('button', { name: 'Add account' }))
    await user.type(within(accounts).getByLabelText('Name'), 'On-call bot')
    await user.type(within(accounts).getByLabelText('App ID'), 'cli_new')
    await user.type(within(accounts).getByLabelText('App Secret'), 's3cret')
    await user.click(within(accounts).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(within(accounts).getAllByRole('listitem')).toHaveLength(3))

    await user.click(within(accounts).getByRole('button', { name: 'Edit Personal test' }))
    expect(within(accounts).getByText('Set')).toBeInTheDocument()
    await user.clear(within(accounts).getByLabelText('Name'))
    await user.type(within(accounts).getByLabelText('Name'), 'Personal test 2')
    await user.click(within(accounts).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(within(accounts).getByText('Personal test 2')).toBeInTheDocument())

    await user.click(within(accounts).getByRole('button', { name: 'Delete Dev group bot' }))
    await user.click(within(await screen.findByRole('alertdialog', { name: 'Delete account Dev group bot?' })).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(within(accounts).queryByText('Dev group bot')).toBeNull())

    const base = `/projects/${defaultProject.id}/plugins/lark-notify/accounts`
    expect(fake.writes().map((r) => [r.method, r.path, r.body])).toEqual([
      ['POST', base, { kind: 'lark', name: 'On-call bot', values: { appId: 'cli_new' }, secrets: { appSecret: 's3cret' } }],
      ['PUT', `${base}/72000000-0000-7000-8000-000000000002`, { name: 'Personal test 2', values: { appId: 'cli_9f8e7d6c5b4a' }, expectedRevision: 1 }],
      ['DELETE', `${base}/72000000-0000-7000-8000-000000000001`, undefined],
    ])
  })

  it('ProjectPluginsPanel embeds the accordion list directly without PageHeader or Sheet chrome', async () => {
    const fake = fakePlugins({ details: [makeDetail()] })
    server.use(...fake.handlers)
    renderWithQuery(
      <MemoryRouter>
        <ProjectPluginsPanel project={defaultProject} />
      </MemoryRouter>,
    )
    expect(await screen.findByRole('link', { name: 'Webhook notifications' })).toBeInTheDocument()
    expect(document.querySelector('[data-page-header]')).toBeNull()
    expect(document.querySelector('[data-sheet]')).toBeNull()
  })
})
