import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { agentParty, consoleParty, externalParty, makeIdentity, makeInboxMessage, makeToken, summaryOf } from '@/test/inbox'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const SECRET = 'lne_0a1b2c3d_5Qm8vR2kT9wXc4LpZ7nB3sYh6JdF1aGe'

interface Backend {
  identities: Schemas['ExternalIdentity'][]
  tokens: Record<string, Schemas['ExternalToken'][]>
  inbox?: Record<string, Schemas['InboxMessage'][]>
}

// Fake external identity backend: the list contains only identities that are not deleted,
// while a single lookup also returns deleted ones; write operations are recorded.
function identityBackend(backend: Backend) {
  const writes: { method: string; path: string; body?: unknown }[] = []
  const handlers = [
    http.get(apiUrl('/external-identities'), () => HttpResponse.json({ identities: backend.identities.filter((i) => !i.deletedAt) })),
    http.post(apiUrl('/external-identities'), async ({ request }) => {
      const body = (await request.json()) as { name: string }
      writes.push({ method: 'POST', path: '/external-identities', body })
      if (backend.identities.some((i) => i.name === body.name && !i.deletedAt)) {
        return HttpResponse.json(errorBody('name_conflict', 'name already used'), { status: 409 })
      }
      const identity = makeIdentity({ name: body.name, createdAt: new Date().toISOString() })
      backend.identities.push(identity)
      backend.tokens[identity.id] = []
      return HttpResponse.json(identity, { status: 201 })
    }),
    http.get(apiUrl('/external-identities/:id'), ({ params }) => {
      const identity = backend.identities.find((i) => i.id === params.id)
      return identity ? HttpResponse.json(identity) : HttpResponse.json(errorBody('not_found'), { status: 404 })
    }),
    http.delete(apiUrl('/external-identities/:id'), ({ params }) => {
      writes.push({ method: 'DELETE', path: `/external-identities/${params.id}` })
      const identity = backend.identities.find((i) => i.id === params.id)!
      identity.deletedAt = new Date().toISOString()
      return new HttpResponse(null, { status: 204 })
    }),
    http.get(apiUrl('/external-identities/:id/tokens'), ({ params }) => HttpResponse.json({ tokens: backend.tokens[String(params.id)] ?? [] })),
    http.post(apiUrl('/external-identities/:id/tokens'), async ({ params, request }) => {
      const body = (await request.json()) as { expiresAt?: string }
      writes.push({ method: 'POST', path: `/external-identities/${params.id}/tokens`, body })
      const token = makeToken({ createdAt: new Date().toISOString(), expiresAt: body.expiresAt ?? null })
      backend.tokens[String(params.id)] = [token, ...(backend.tokens[String(params.id)] ?? [])]
      return HttpResponse.json({ token, secret: SECRET }, { status: 201 })
    }),
    http.delete(apiUrl('/external-identities/:id/tokens/:tokenId'), ({ params }) => {
      writes.push({ method: 'DELETE', path: `/external-identities/${params.id}/tokens/${params.tokenId}` })
      const token = backend.tokens[String(params.id)].find((k) => k.id === params.tokenId)!
      token.revokedAt = new Date().toISOString()
      return new HttpResponse(null, { status: 204 })
    }),
    http.get(apiUrl('/external-identities/:id/inbox'), ({ params, request }) => {
      const url = new URL(request.url)
      const direction = url.searchParams.get('direction') ?? 'all'
      const unread = url.searchParams.get('unread') === 'true'
      const list = (backend.inbox?.[String(params.id)] ?? []).filter((m) => {
        const received = m.to.id === params.id
        if (direction === 'received' && !received) return false
        if (direction === 'sent' && received) return false
        return !unread || !m.readAt
      })
      return HttpResponse.json({ messages: list.map(summaryOf), nextCursor: null })
    }),
    http.get(apiUrl('/external-identities/:id/inbox/:messageId'), ({ params }) =>
      HttpResponse.json(backend.inbox?.[String(params.id)]?.find((m) => m.id === params.messageId)),
    ),
    http.get(apiUrl('/inbox/threads/:threadId'), ({ params }) => {
      const messages = Object.values(backend.inbox ?? {})
        .flat()
        .filter((m) => m.threadId === params.threadId)
      return HttpResponse.json({ id: params.threadId, messages })
    }),
  ]
  return { handlers, writes }
}

async function renderIdentities(backend: Backend, path = '/identities') {
  const { handlers, writes } = identityBackend(backend)
  server.use(...handlers, ...baseHandlers([]))
  const user = userEvent.setup()
  const result = renderRoutes(routes, path)
  await screen.findByRole('heading', { name: /^External identities/, level: 1 })
  return { ...result, user, writes }
}

const list = () => within(screen.getByRole('complementary', { name: 'External identities' }))
const pageHeader = () => within(screen.getByRole('heading', { level: 1, name: 'External identities' }).closest('header')!)
const tokenTable = async () => within(await screen.findByRole('table'))

describe('Create external identity', () => {
  it('without external identities says the list is empty; after ci-bot is created it opens its Tokens & Access tab', async () => {
    const { user, writes, router } = await renderIdentities({ identities: [], tokens: {} })
    expect(await screen.findByText('No external identities yet.')).toBeInTheDocument()
    await user.click(pageHeader().getByRole('button', { name: 'New' }))
    const dialog = within(await screen.findByRole('dialog', { name: 'New external identity' }))
    expect(dialog.getByText(/Can't be console or platform/)).toBeInTheDocument()
    await user.type(dialog.getByRole('textbox', { name: 'Name' }), 'ci-bot')
    await user.click(dialog.getByRole('button', { name: 'Create' }))
    await waitFor(() => expect(writes).toEqual([{ method: 'POST', path: '/external-identities', body: { name: 'ci-bot' } }]))
    await waitFor(() => expect(list().getByRole('link', { name: /ci-bot/ })).toHaveAttribute('aria-current', 'page'))
    expect(router.state.location.pathname).toMatch(/^\/identities\/.+\/tokens$/)
    expect(await screen.findByRole('heading', { name: 'ci-bot', level: 2 })).toBeInTheDocument()
    expect(await screen.findByRole('button', { name: 'Issue token' })).toBeInTheDocument()
  })

  it('does not submit a name that breaks the rules or is reserved, and says why', async () => {
    const { user, writes } = await renderIdentities({ identities: [], tokens: {} })
    await user.click(pageHeader().getByRole('button', { name: 'New' }))
    const dialog = within(await screen.findByRole('dialog', { name: 'New external identity' }))
    const input = dialog.getByRole('textbox', { name: 'Name' })
    await user.type(input, 'platform')
    await user.click(dialog.getByRole('button', { name: 'Create' }))
    expect(dialog.getByRole('alert')).toHaveTextContent("The name can't be platform; it's reserved")
    await user.clear(input)
    await user.type(input, 'CI Bot')
    await user.click(dialog.getByRole('button', { name: 'Create' }))
    expect(dialog.getByRole('alert')).toHaveTextContent('Use only lowercase letters')
    expect(writes).toEqual([])
  })

  it('duplicate name: shows the reason next to the name and keeps what was typed', async () => {
    const { user } = await renderIdentities({ identities: [makeIdentity({ name: 'ci-bot' })], tokens: {} })
    await user.click(pageHeader().getByRole('button', { name: 'New' }))
    const dialog = within(await screen.findByRole('dialog', { name: 'New external identity' }))
    await user.type(dialog.getByRole('textbox', { name: 'Name' }), 'ci-bot')
    await user.click(dialog.getByRole('button', { name: 'Create' }))
    expect(await dialog.findByRole('alert')).toHaveTextContent('That name is already taken')
    expect(dialog.getByRole('textbox', { name: 'Name' })).toHaveValue('ci-bot')
  })
})

describe('Delete external identity', () => {
  it('the Danger Zone lives in Settings and confirms four consequences before deleting', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const backend: Backend = { identities: [ciBot, makeIdentity({ name: 'deploy-watcher' })], tokens: { [ciBot.id]: [makeToken(), makeToken()] } }
    const { user, writes } = await renderIdentities(backend, `/identities/${ciBot.id}`)
    expect(await list().findByRole('link', { name: /ci-bot/ })).toBeInTheDocument()
    // Delete is not mounted in the persistent header on Messages.
    expect(screen.queryByRole('button', { name: 'Delete external identity' })).toBeNull()

    await user.click(screen.getByRole('link', { name: 'Settings' }))
    await user.click(await screen.findByRole('button', { name: 'Delete external identity' }))
    const confirm = within(await screen.findByRole('alertdialog', { name: 'Delete external identity "ci-bot"?' }))
    expect(confirm.getAllByRole('listitem').map((item) => item.textContent)).toEqual([
      'all its tokens are revoked at once;',
      'messages it sent and received are kept and shown as from a deleted identity;',
      'messages sent to it afterwards are rejected;',
      'the name is freed, so an identity with the same name can be created again.',
    ])
    await user.click(confirm.getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(writes).toEqual([{ method: 'DELETE', path: `/external-identities/${ciBot.id}` }]))
    await waitFor(() => expect(list().queryByRole('link', { name: /ci-bot/ })).toBeNull())
    expect(list().getByRole('link', { name: /deploy-watcher/ })).toBeInTheDocument()
  })

  it('deleted identity: shows only Messages with the read-only inbox and deletion time, requests no tokens, and cannot issue, revoke or delete', async () => {
    const gone = makeIdentity({ name: 'release-bot', deletedAt: '2026-10-11T02:00:00Z' })
    const inbox = { [gone.id]: [makeInboxMessage({ from: externalParty(gone, true), to: consoleParty, text: 'v0.1.0 released', readAt: '2026-10-11T01:00:00Z' })] }
    await renderIdentities({ identities: [gone], tokens: {}, inbox }, `/identities/${gone.id}`)
    expect(await screen.findByRole('heading', { name: 'release-bot', level: 2 })).toBeInTheDocument()
    expect(screen.getByText('Deleted identity')).toBeInTheDocument()
    expect(screen.getByText(/^Deleted \d/)).toBeInTheDocument()
    expect(await screen.findByText('v0.1.0 released')).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: /Tokens & Access/ })).toBeNull()
    expect(screen.queryByRole('link', { name: 'Settings' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Issue token' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Delete external identity' })).toBeNull()
    expect(screen.queryByRole('table')).toBeNull()
  })
})

describe('Issue token', () => {
  it('issue and copy: the plaintext and the risk note show only once, closing asks for confirmation; after closing the plaintext is nowhere in the page, the URL, browser storage or the query cache', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const { user, writes, client, router } = await renderIdentities({ identities: [ciBot], tokens: { [ciBot.id]: [] } }, `/identities/${ciBot.id}/tokens`)
    expect(await screen.findByRole('note')).toHaveTextContent(/A token has every permission of the external CLI.*A leaked token hands control of the platform to whoever has it/)
    await user.click(screen.getByRole('button', { name: 'Issue token' }))
    const issue = within(await screen.findByRole('dialog', { name: 'Issue a token for ci-bot' }))
    expect(issue.getByRole('radio', { name: '30 days' })).toBeChecked()
    expect(issue.getByText(/^Expires /)).toBeInTheDocument()
    expect(issue.getByText(/A token has every permission of the external CLI/)).toBeInTheDocument()
    const before = Date.now()
    await user.click(issue.getByRole('button', { name: 'Issue' }))
    const result = within(await screen.findByRole('dialog', { name: 'Token issued' }))
    const expiresAt = (writes[0].body as { expiresAt: string }).expiresAt
    const days = (Date.parse(expiresAt) - before) / 86_400_000
    expect(days).toBeGreaterThan(29.99)
    expect(days).toBeLessThan(30.01)
    expect(result.getByText(SECRET)).toBeInTheDocument()
    expect(result.getByRole('alert')).toHaveTextContent("This is the only time the token is shown in full.Once you close this, you can't see it again. Copy it now and keep it safe.")
    expect(result.getByText(/A token has every permission of the external CLI/)).toBeInTheDocument()
    await user.click(result.getByRole('button', { name: 'Copy' }))
    await expect(navigator.clipboard.readText()).resolves.toBe(SECRET)
    // Closing needs confirmation; choosing Back keeps the plaintext.
    await user.click(result.getByRole('button', { name: "I've saved it, close" }))
    const confirm = within(await screen.findByRole('alertdialog', { name: 'Have you saved the token?' }))
    expect(confirm.getByText("Once closed, this token's full value won't be shown again and can't be recovered.")).toBeInTheDocument()
    await user.click(confirm.getByRole('button', { name: 'Back' }))
    expect(screen.getByText(SECRET)).toBeInTheDocument()
    await user.keyboard('{Escape}')
    await user.click(within((await screen.findByRole('alertdialog')).querySelector<HTMLElement>('[data-slot="alert-dialog-footer"]')!).getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.queryByText(SECRET)).toBeNull())
    expect(document.body.innerHTML).not.toContain(SECRET)
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.state.data))).not.toContain(SECRET)
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.data))).not.toContain(SECRET)
    expect(JSON.stringify({ ...window.localStorage })).not.toContain(SECRET)
    expect(JSON.stringify({ ...window.sessionStorage })).not.toContain(SECRET)
    expect(router.state.location.pathname + router.state.location.search + router.state.location.hash).not.toContain(SECRET)
    const table = await tokenTable()
    expect(table.getAllByRole('row')).toHaveLength(2)
    expect(table.getByText('Active')).toBeInTheDocument()
    expect(table.queryByText(SECRET)).toBeNull()
  })

  it('token shown only once: a sticky-note warning, the plaintext in a dashed box with a yellow Copy next to it; the close button in the top right also asks to confirm it was saved', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const { user } = await renderIdentities({ identities: [ciBot], tokens: { [ciBot.id]: [] } }, `/identities/${ciBot.id}/tokens`)
    await user.click(await screen.findByRole('button', { name: 'Issue token' }))
    await user.click(within(await screen.findByRole('dialog', { name: 'Issue a token for ci-bot' })).getByRole('button', { name: 'Issue' }))
    const box = await screen.findByRole('dialog', { name: 'Token issued' })
    const result = within(box)
    expect(box.querySelector('[data-dialog-tab]')).toHaveTextContent('Token')
    expect(result.getByRole('alert')).toHaveClass('riso-tilt')
    const copy = result.getByRole('button', { name: 'Copy' })
    expect(copy).toHaveClass('[[data-skin=plate]_&]:bg-riso-yellow')
    expect(copy.parentElement).toHaveClass('bg-code-bg', 'border', 'border-code-border', '[[data-skin=plate]_&]:border-dashed')
    await user.click(result.getByRole('button', { name: 'Close' }))
    expect(await screen.findByRole('alertdialog', { name: 'Have you saved the token?' })).toBeInTheDocument()
    expect(box).toBeInTheDocument()
    expect(within(box).getByText(SECRET)).toBeInTheDocument()
  })

  it('choosing Never expires submits no expiry; a picked date means the end of that day, and a date before today is not submitted', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const { user, writes } = await renderIdentities({ identities: [ciBot], tokens: { [ciBot.id]: [] } }, `/identities/${ciBot.id}/tokens`)
    await user.click(await screen.findByRole('button', { name: 'Issue token' }))
    let issue = within(await screen.findByRole('dialog', { name: 'Issue a token for ci-bot' }))
    await user.click(issue.getByRole('radio', { name: 'Never expires' }))
    expect(issue.getByText("The token won't expire on its own; it can only be revoked by hand.")).toBeInTheDocument()
    await user.click(issue.getByRole('button', { name: 'Issue' }))
    await waitFor(() => expect(writes.at(-1)?.body).toEqual({}))
    await user.click(within(await screen.findByRole('dialog', { name: 'Token issued' })).getByRole('button', { name: "I've saved it, close" }))
    await user.click(within((await screen.findByRole('alertdialog')).querySelector<HTMLElement>('[data-slot="alert-dialog-footer"]')!).getByRole('button', { name: 'Close' }))
    await user.click(await screen.findByRole('button', { name: 'Issue token' }))
    issue = within(await screen.findByRole('dialog', { name: 'Issue a token for ci-bot' }))
    await user.click(issue.getByRole('radio', { name: 'Pick a date' }))
    const date = issue.getByLabelText('Expiry date (expires at the end of that day)')
    await user.type(date, '2020-01-01')
    await user.click(issue.getByRole('button', { name: 'Issue' }))
    expect(issue.getByRole('alert')).toHaveTextContent('Pick today or a later date')
    expect(writes).toHaveLength(1)
    await user.clear(date)
    await user.type(date, '2099-12-31')
    await user.click(issue.getByRole('button', { name: 'Issue' }))
    await waitFor(() => expect(writes).toHaveLength(2))
    expect(new Date((writes[1].body as { expiresAt: string }).expiresAt).getTime()).toBe(new Date(2099, 11, 31, 23, 59, 59, 999).getTime())
  })
})

describe('View, filter and revoke tokens', () => {
  it('shows metadata, filters by Active | All, and revokes one token without affecting others', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const first = makeToken({ lastUsedAt: '2026-10-12T06:02:00Z' })
    const second = makeToken({ expiresAt: '2099-01-01T00:00:00Z' })
    const old = makeToken({ expiresAt: '2026-09-08T00:30:00Z' })
    const { user, writes } = await renderIdentities({ identities: [ciBot], tokens: { [ciBot.id]: [first, second, old] } }, `/identities/${ciBot.id}/tokens`)
    const table = await tokenTable()
    expect(table.getAllByRole('columnheader').map((cell) => cell.textContent)).toEqual(['ID', 'Issued', 'Expires', 'Last used', 'Status', 'Actions'])
    const rowOf = (prefix: string) => table.getByText(prefix).closest('tr')!
    expect(rowOf(first.prefix)).toHaveTextContent(/Never expires.*Active/)
    expect(rowOf(old.prefix)).toHaveTextContent(/Never used.*Expired/)
    expect(within(rowOf(old.prefix)).queryByRole('button')).toBeNull()
    expect(list().getByRole('link', { name: /ci-bot/ })).toHaveTextContent('2 active tokens')

    // Inline Active | All status filter hides expired tokens when Active is selected.
    await user.click(screen.getByRole('radio', { name: /^Active/ }))
    expect(table.queryByText(old.prefix)).toBeNull()
    expect(table.getByText(first.prefix)).toBeInTheDocument()
    await user.click(screen.getByRole('radio', { name: /^All/ }))
    expect(table.getByText(old.prefix)).toBeInTheDocument()

    await user.click(within(rowOf(first.prefix)).getByRole('button', { name: `Revoke ${first.prefix}` }))
    const confirm = within(await screen.findByRole('alertdialog', { name: `Revoke token ${first.prefix}?` }))
    expect(confirm.getByText(/ci-bot's other tokens are not affected/)).toBeInTheDocument()
    await user.click(confirm.getByRole('button', { name: 'Revoke' }))
    await waitFor(() => expect(writes).toEqual([{ method: 'DELETE', path: `/external-identities/${ciBot.id}/tokens/${first.id}` }]))
    await waitFor(() => expect(rowOf(first.prefix)).toHaveTextContent('Revoked'))
    expect(rowOf(second.prefix)).toHaveTextContent('Active')
    expect(within(rowOf(second.prefix)).getByRole('button', { name: `Revoke ${second.prefix}` })).toBeInTheDocument()
  })
})

describe('Read-only inbox of an external identity', () => {
  it('switches to All activity in Messages to list received and sent rows; filters by direction and unread; opening full text does not change read state', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const reviewer = makeAgent({ name: 'Code reviewer', address: 'test-org/test-project/Code reviewer' })
    const gotIt = makeInboxMessage({ from: agentParty(reviewer), to: externalParty(ciBot), text: 'Fix merged; please rerun the nightly build.' })
    const sent = makeInboxMessage({ from: externalParty(ciBot), to: consoleParty, text: 'Nightly build failed: 1 test in naming did not pass.', readAt: '2026-10-12T09:00:00Z' })
    const { user } = await renderIdentities({ identities: [ciBot], tokens: { [ciBot.id]: [] }, inbox: { [ciBot.id]: [gotIt, sent] } }, `/identities/${ciBot.id}`)
    await user.click(await screen.findByRole('radio', { name: 'All activity' }))

    const inboxList = () => within(screen.getByRole('list', { name: 'Inbox' }))
    await screen.findByRole('list', { name: 'Inbox' })
    const items = inboxList().getAllByRole('listitem')
    expect(items[0]).toHaveTextContent(/^ReceivedCode reviewer.*Fix merged.*Unread$/)
    expect(items[1]).toHaveTextContent(/^SentConsole@console.*Read$/)
    expect(screen.getByText("Read-only; doesn't change read status")).toBeInTheDocument()
    await user.click(screen.getByRole('radio', { name: 'Sent' }))
    await waitFor(() => expect(inboxList().getAllByRole('listitem')).toHaveLength(1))
    expect(inboxList().getByRole('listitem')).toHaveTextContent('Sent')
    await user.click(screen.getByRole('checkbox', { name: 'Unread only' }))
    expect(await screen.findByText('No matching messages.')).toBeInTheDocument()
    await user.click(screen.getByRole('radio', { name: 'All' }))
    await screen.findByRole('list', { name: 'Inbox' })
    await user.click(inboxList().getByRole('button', { name: /Code reviewer/ }))
    expect(await inboxList().findByText('Fix merged; please rerun the nightly build.', { selector: 'p' })).toBeInTheDocument()
    expect(within(inboxList().getByRole('button', { name: /Code reviewer/ })).getByText('Unread')).toBeInTheDocument()
  })
})

describe('External identities on the sheet', () => {
  it('the header has breadcrumb "Global›External identities", count, and right-aligned New button', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    await renderIdentities({ identities: [ciBot], tokens: { [ciBot.id]: [] } }, `/identities/${ciBot.id}`)
    const header = screen.getByRole('heading', { level: 1, name: 'External identities' }).closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›External identities')
    expect(await within(header).findByText('1 identity')).toBeInTheDocument()
    expect(within(header).getByRole('button', { name: 'New' })).toHaveClass(
      'bg-surface-hover',
      '[[data-skin=plate]_&]:border-2',
      '[[data-skin=plate]_&]:border-ink',
      '[[data-skin=plate]_&]:bg-riso-pink',
    )
  })

  it('list and details sit on one sheet and switching identities preserves the active tab', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const release = makeIdentity({ name: 'release-bot' })
    const { user, router } = await renderIdentities(
      { identities: [ciBot, release], tokens: { [ciBot.id]: [], [release.id]: [] } },
      `/identities/${ciBot.id}/tokens`,
    )
    await screen.findByRole('heading', { level: 2, name: 'ci-bot' })
    const aside = screen.getByRole('complementary', { name: 'External identities' })
    expect(aside.closest('[data-sheet]')).toContainElement(screen.getByRole('heading', { level: 2, name: 'ci-bot' }))
    expect(list().getByRole('link', { name: /ci-bot/ })).toHaveClass(
      'bg-surface-active',
      'border-hairline-strong',
      '[[data-skin=plate]_&]:bg-riso-yellow',
      '[[data-skin=plate]_&]:border-ink',
      '[[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink)]',
    )
    expect(list().getByRole('link', { name: /release-bot/ })).not.toHaveClass('bg-surface-active')

    await user.click(list().getByRole('link', { name: /release-bot/ }))
    await waitFor(() => expect(router.state.location.pathname).toBe(`/identities/${release.id}/tokens`))
    expect(await screen.findByRole('heading', { level: 2, name: 'release-bot' })).toBeInTheDocument()
  })

  it('preserves mono and Plate styling on the identity header, sticky token risk note and Danger Zone button', async () => {
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const { user } = await renderIdentities({ identities: [ciBot], tokens: { [ciBot.id]: [] } }, `/identities/${ciBot.id}/tokens`)
    const name = await screen.findByRole('heading', { level: 2, name: 'ci-bot' })
    expect(name).toHaveClass('riso-marker', 'font-display-cn')
    expect(screen.getByText(ciBot.address)).toHaveClass('font-mono')
    expect(await screen.findByRole('note')).toHaveClass('riso-tilt')

    await user.click(screen.getByRole('link', { name: 'Settings' }))
    expect(await screen.findByRole('button', { name: 'Delete external identity' })).toHaveClass(
      'border',
      'text-red-text',
      '[[data-skin=plate]_&]:border-2',
      '[[data-skin=plate]_&]:border-red-text',
    )
  })
})
