import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { defaultProject } from '@/test/api'
import { fakePlugins, larkAccountKind, makeAccount, makeComponents, makeConfig, makeDelivery, makeDetail, makeManifest, makePlugin, makeProjectPlugin, makeResolvedSource } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

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

const card = async (name: string) => (await screen.findByRole('link', { name })).closest('li')!

describe('Project plugins page', () => {
  it('one card per installed plugin with an enable switch; a plugin with only an Agent side says to grant it in the Agent config', async () => {
    renderProject()
    const webhook = await card('Webhook 通知')
    expect(within(webhook).getByRole('switch', { name: 'Enable Webhook 通知 in this project' })).toHaveAttribute('aria-checked', 'true')
    const playwright = await card('Dedicated browser')
    expect(within(playwright).getByRole('switch', { name: 'Enable Dedicated browser in this project' })).toHaveAttribute('aria-checked', 'false')
    await waitFor(() => expect(playwright).toHaveTextContent('Agent side only. After enabling, grant it in an Agent\'s config.'))
  })

  it('enabling sends an empty project config and revision 0', async () => {
    const user = userEvent.setup()
    const { fake } = renderProject()
    const playwright = await card('Dedicated browser')
    await user.click(within(playwright).getByRole('switch', { name: 'Enable Dedicated browser in this project' }))
    await waitFor(() => expect(fake.writes()).toEqual([{ method: 'PUT', path: `/projects/${defaultProject.id}/plugins/playwright`, query: {}, body: { enabled: true, values: {}, expectedRevision: 0 } }]))
    await waitFor(() => expect(within(playwright).getByRole('switch', { name: 'Enable Dedicated browser in this project' })).toHaveAttribute('aria-checked', 'true'))
  })

  it('project config has only x-scope: project fields and shows the global value; saving keeps it enabled and sends the revision', async () => {
    const user = userEvent.setup()
    const { fake } = renderProject()
    const webhook = await card('Webhook 通知')
    const url = await within(webhook).findByLabelText('回调地址')
    expect(within(webhook).queryByLabelText('重试上限')).toBeNull()
    expect(webhook).toHaveTextContent('Global value: https://hooks.example.com/loop-now')
    await user.type(url, 'https://hooks.example.com/propanda')
    await user.click(within(webhook).getByRole('button', { name: 'Save' }))
    await waitFor(() =>
      expect(fake.writes()).toEqual([{ method: 'PUT', path: `/projects/${defaultProject.id}/plugins/webhook`, query: {}, body: { enabled: true, values: { url: 'https://hooks.example.com/propanda' }, expectedRevision: 1 } }]),
    )
  })

  it('lists only the events of this project', async () => {
    const { fake } = renderProject()
    const webhook = await card('Webhook 通知')
    await waitFor(() => expect(within(webhook).getAllByText('inbox.received')).toHaveLength(1))
    expect(within(webhook).queryByText('run.ended')).toBeNull()
    expect(fake.requests.find((r) => r.path === '/plugins/webhook/deliveries')?.query).toEqual({ limit: '3', projectId: defaultProject.id })
  })

  it('accounts: lists the name and the start of the App ID; secrets are write-only when adding; edits send the revision; deleting asks first', async () => {
    const user = userEvent.setup()
    const { fake } = renderProject()
    const larkCard = await card('Lark notifications')
    const accounts = await within(larkCard).findByRole('region', { name: 'Accounts · 飞书应用' })
    await waitFor(() => expect(within(accounts).getAllByRole('listitem').map((item) => item.textContent)).toEqual(['研发群机器人cli_a1b2EditDelete', 'Personal testcli_9f8e7d…EditDelete']))

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

    await user.click(within(accounts).getByRole('button', { name: 'Delete 研发群机器人' }))
    await user.click(within(await screen.findByRole('alertdialog', { name: 'Delete account 研发群机器人?' })).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(within(accounts).queryByText('研发群机器人')).toBeNull())

    const base = `/projects/${defaultProject.id}/plugins/lark-notify/accounts`
    expect(fake.writes().map((r) => [r.method, r.path, r.body])).toEqual([
      ['POST', base, { kind: 'lark', name: 'On-call bot', values: { appId: 'cli_new' }, secrets: { appSecret: 's3cret' } }],
      ['PUT', `${base}/72000000-0000-7000-8000-000000000002`, { name: 'Personal test 2', values: { appId: 'cli_9f8e7d6c5b4a' }, expectedRevision: 1 }],
      ['DELETE', `${base}/72000000-0000-7000-8000-000000000001`, undefined],
    ])
  })
})
