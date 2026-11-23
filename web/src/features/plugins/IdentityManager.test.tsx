import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { apiUrl, errorBody } from '@/test/api'
import { makeIdentity } from '@/test/inbox'
import { fakePlugins, makeComponents, makeDetail, makeManifest, makePlugin, makePreview } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

// One webhook channel can manage external identities; the lark-notify plugin's channel cannot (it does not appear among the choices).
const webhook = makeDetail(makePlugin(), makeManifest({ permissions: ['state', 'identities.manage'], components: makeComponents({ channels: [{ id: 'hooks', label: 'Callback', manageIdentities: true }] }) }))
const lark = makeDetail(
  makePlugin({ id: 'lark-notify', displayName: 'Lark notifications', version: '0.3.1' }),
  makeManifest({ id: 'lark-notify', displayName: 'Lark notifications', version: '0.3.1', components: makeComponents({ channels: [{ id: 'lark', label: 'Lark', manageIdentities: false }] }) }),
)

function setup(identity: Schemas['ExternalIdentity'], plugins = [webhook, lark], manager?: (body: { managedBy: Schemas['ExternalIdentityManager'] | null }) => Response | undefined) {
  const fake = fakePlugins({ details: plugins })
  const writes: unknown[] = []
  let current = identity
  server.use(
    ...fake.handlers,
    ...baseHandlers([]),
    http.get(apiUrl('/external-identities'), () => HttpResponse.json({ identities: [current] })),
    http.get(apiUrl('/external-identities/:id'), () => HttpResponse.json(current)),
    http.get(apiUrl('/external-identities/:id/tokens'), () => HttpResponse.json({ tokens: [] })),
    http.get(apiUrl('/external-identities/:id/inbox'), () => HttpResponse.json({ messages: [], nextCursor: null })),
    http.put(apiUrl('/external-identities/:id/manager'), async ({ request }) => {
      const body = (await request.json()) as { managedBy: Schemas['ExternalIdentityManager'] | null }
      writes.push(body)
      const custom = manager?.(body)
      if (custom) return custom
      current = { ...current, managedBy: body.managedBy }
      return HttpResponse.json(current)
    }),
  )
  renderRoutes(routes, `/identities/${identity.id}`)
  return { writes, fake }
}

const section = () => screen.findByRole('region', { name: 'Managed by' })

describe('Managing external identities', () => {
  it('lists the plugin channels that declare managing; choosing one and saving shows who manages it now', async () => {
    const user = userEvent.setup()
    const { writes } = setup(makeIdentity())
    const box = await section()
    const select = within(box).getByLabelText('Managing plugin channel')
    await waitFor(() => expect(within(select).getAllByRole('option').map((o) => o.textContent)).toEqual(['Not managed', 'Webhook notifications · Callback']))
    expect(within(box).getByRole('button', { name: 'Save' })).toBeDisabled()
    await user.selectOptions(select, 'Webhook notifications · Callback')
    await user.click(within(box).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(writes).toEqual([{ managedBy: { pluginId: 'webhook', channelId: 'hooks' } }]))
    expect(await within(box).findByRole('status')).toHaveTextContent('Managed by Webhook notifications · Callback')
  })

  it('switching back to not managed saves null', async () => {
    const user = userEvent.setup()
    const { writes } = setup(makeIdentity({ managedBy: { pluginId: 'webhook', channelId: 'hooks' } }))
    const box = await section()
    const select = within(box).getByLabelText('Managing plugin channel')
    await waitFor(() => expect(select).toHaveDisplayValue('Webhook notifications · Callback'))
    await user.selectOptions(select, 'Not managed')
    await user.click(within(box).getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(writes).toEqual([{ managedBy: null }]))
    await waitFor(() => expect(within(box).queryByRole('status')).toBeNull())
  })

  it('the managing plugin is not running: says so and keeps the binding', async () => {
    setup(makeIdentity({ managedBy: { pluginId: 'webhook', channelId: 'hooks' } }), [makeDetail(makePlugin({ status: 'host_stopped' }), webhook.manifest), lark])
    const box = await section()
    expect(await within(box).findByText(/The managing plugin isn't running \(Host stopped\)/)).toBeInTheDocument()
    expect(within(box).getByRole('link', { name: 'View plugin' })).toHaveAttribute('href', '/plugins/webhook')
  })

  it('shows the reason when saving is rejected (422, the channel no longer declares managing)', async () => {
    const user = userEvent.setup()
    setup(makeIdentity(), [webhook, lark], () => HttpResponse.json(errorBody('validation_failed', 'This channel cannot host external identities'), { status: 422 }))
    const box = await section()
    await waitFor(() => expect(within(box).getAllByRole('option')).toHaveLength(2))
    await user.selectOptions(within(box).getByLabelText('Managing plugin channel'), 'Webhook notifications · Callback')
    await user.click(within(box).getByRole('button', { name: 'Save' }))
    expect(await within(box).findByText(/This channel cannot host external identities/)).toBeInTheDocument()
  })

  it('says so when no plugin can manage identities, with a link to the plugin market', async () => {
    setup(makeIdentity(), [lark])
    const box = await section()
    expect(await within(box).findByText('No plugins can manage external identities yet.')).toBeInTheDocument()
    expect(within(box).getByRole('link', { name: 'Plugin market' })).toHaveAttribute('href', '/plugins')
  })
})

describe('"Can manage external identities" on plugin pages', () => {
  it('the install confirmation and "What it contains" in plugin details both list the managing channels, and the card has a tag', async () => {
    const user = userEvent.setup()
    const fake = fakePlugins({ details: [webhook] })
    server.use(...fake.handlers, ...baseHandlers([]), http.post(apiUrl('/plugin-previews'), () => HttpResponse.json(makePreview({ manifest: { ...webhook.manifest, version: '1.3.0' }, installedVersion: '1.2.0' }), { status: 201 })))
    const { router } = renderRoutes(routes, '/plugins')
    const card = (await screen.findByRole('link', { name: 'Webhook notifications' })).closest('li')!
    await waitFor(() => expect(card).toHaveTextContent('Can manage external identities'))
    await router.navigate('/plugins/webhook')
    const contains = await screen.findByRole('region', { name: 'What it contains' })
    expect(within(contains).getByText('Can manage external identities').parentElement).toHaveTextContent('Can manage external identities · Callback · Pushes messages sent to external identities out, and sends replies back as that identity')
    expect(within(contains).getByText('identities.manage').parentElement).toHaveTextContent('Manage external identities: read their Inbox, reply and answer in their name')
    await user.click(screen.getByRole('button', { name: 'Check for updates' }))
    const dialog = await screen.findByRole('dialog', { name: /Update Webhook notifications/ })
    expect(within(dialog).getByText('Can manage external identities').parentElement).toHaveTextContent('Can manage external identities · Callback')
    expect(within(dialog).getByText('identities.manage')).toBeInTheDocument()
  })
})
