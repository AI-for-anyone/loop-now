import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultAgentsPath, makeAgent, platformAgentsUrl, toListItem } from '@/test/api'
import { agentParty, consoleParty, emitInbox, externalParty, makeIdentity, makeInboxMessage, openInboxStream, summaryOf } from '@/test/inbox'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const agent = makeAgent({ name: 'Code review assistant', address: 'test-org/test-project/Code review assistant' })
const tester = makeAgent({ name: 'Testing assistant', address: 'test-org/test-project/Testing assistant' })

// Open the Agent's Inbox tab and record every request to the inbox endpoints (method, path and query parameters).
async function renderAgentInbox(messages: Schemas['InboxMessage'][]) {
  const requests: string[] = []
  server.use(
    http.all(apiUrl('/inbox/*'), ({ request }) => {
      requests.push(`${request.method} ${new URL(request.url).pathname}`)
      return HttpResponse.json({}, { status: 500 })
    }),
    http.get(apiUrl(`/agents/${agent.id}/inbox`), ({ request }) => {
      const url = new URL(request.url)
      requests.push(`GET ${url.pathname}${url.search}`)
      const unread = url.searchParams.get('unread') === 'true'
      const list = messages.filter((m) => !unread || !m.readAt).map(summaryOf)
      return HttpResponse.json({ messages: list, nextCursor: null })
    }),
    http.get(apiUrl(`/agents/${agent.id}/inbox/:messageId`), ({ request, params }) => {
      requests.push(`GET ${new URL(request.url).pathname}`)
      return HttpResponse.json(messages.find((m) => m.id === params.messageId))
    }),
    ...baseHandlers([agent, tester]),
  )
  const user = userEvent.setup()
  const result = renderRoutes(routes, `${defaultAgentsPath}/${agent.id}/inbox`)
  const list = within(await screen.findByRole('list', { name: 'Messages received by Code review assistant' }))
  return { ...result, user, list, requests }
}

const received = () => [
  makeInboxMessage({ from: agentParty(tester), to: agentParty(agent), text: 'I added two tests for the naming package, give them a run.' }),
  makeInboxMessage({ from: consoleParty, to: agentParty(agent), text: 'Please review the naming package', whenBusy: 'interrupt', readAt: new Date().toISOString() }),
]

describe("Each Agent's inbox is read-only", () => {
  it('a queued message someone cancelled is marked as cancelled by someone and does not count as unread', async () => {
    const cancelled = makeInboxMessage({ from: agentParty(tester), to: agentParty(agent), text: 'No need to handle this one', cancelledAt: '2026-10-25T08:00:00Z' })
    const { list } = await renderAgentInbox([cancelled])
    const row = list.getByRole('button', { name: /Testing assistant/ })
    expect(within(row).getByText('Cancelled by someone')).toBeInTheDocument()
    expect(within(row).queryByText('Unread')).toBeNull()
  })

  it('viewing the messages an Agent received: lists the sender, read or unread, and marks interrupting messages', async () => {
    const [first, second] = received()
    const { list } = await renderAgentInbox([second, first])
    const items = list.getAllByRole('listitem')
    expect(items).toHaveLength(2)
    expect(items[0]).toHaveTextContent(/Console@consoleInterrupt.*Read \d{2}:\d{2}/)
    expect(items[1]).toHaveTextContent(/Testing assistanttest-org\/test-project\/Testing assistant.*I added two tests for the naming package.*Unread$/)
    const badge = screen.getByLabelText("Read-only view: opening a message doesn't mark it read")
    expect(badge).toHaveTextContent('Read-only')
    expect(badge).toHaveAttribute('title', "Read-only view: opening a message doesn't mark it read")
  })

  it('unread only: the list has only the messages the Agent has not read yet', async () => {
    const [first, second] = received()
    const { user, requests } = await renderAgentInbox([second, first])
    await user.click(screen.getByRole('radio', { name: /^Unread only/ }))
    await waitFor(() => expect(requests).toContain(`GET /api/v1/agents/${agent.id}/inbox?limit=20&unread=true`))
    const list = within(await screen.findByRole('list', { name: 'Messages received by Code review assistant' }))
    await waitFor(() => expect(list.getAllByRole('listitem')).toHaveLength(1))
    expect(list.getByRole('listitem')).toHaveTextContent('Testing assistant')
  })

  it('unread only with no unread messages says there are no unread messages', async () => {
    const [, second] = received()
    const { user } = await renderAgentInbox([second])
    await user.click(screen.getByRole('radio', { name: /^Unread only/ }))
    expect(await screen.findByText('No unread messages.')).toBeInTheDocument()
  })

  it('viewing the full text does not change the read state: only the read-only full-text endpoint is requested, and there is no mark-as-read action', async () => {
    const [first] = received()
    const { user, list, requests } = await renderAgentInbox([first])
    await user.click(list.getByRole('button', { name: /Testing assistant/ }))
    expect(await list.findByText('I added two tests for the naming package, give them a run.', { selector: 'p' })).toBeInTheDocument()
    expect(requests.filter((r) => !r.startsWith('GET'))).toEqual([])
    expect(requests).toContain(`GET /api/v1/agents/${agent.id}/inbox/${first.id}`)
    expect(within(list.getByRole('button', { name: /Testing assistant/ })).getByText('Unread')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /mark (as )?(read|unread)/i })).toBeNull()
  })

  it('after the Agent reads a message, the read state changes live with the inbox event stream', async () => {
    const [first] = received()
    const { list } = await renderAgentInbox([first])
    await openInboxStream(0)
    const readAt = new Date().toISOString()
    first.readAt = readAt
    await emitInbox('inbox.message.read', { id: first.id, threadId: first.threadId, to: agentParty(agent), readAt, unreadTotal: 0 })
    await waitFor(() => expect(list.getByRole('listitem')).toHaveTextContent(/Read \d{2}:\d{2}$/))
  })

  it('the list updates when a new message arrives, also for a message that involves neither another Agent nor the operator', async () => {
    const [first] = received()
    const messages = [first]
    const { list } = await renderAgentInbox(messages)
    await openInboxStream(0)
    const ciBot = makeIdentity({ name: 'ci-bot' })
    const fresh = makeInboxMessage({ from: externalParty(ciBot), to: agentParty(agent), text: 'The nightly build failed' })
    messages.unshift(fresh)
    await emitInbox('inbox.message.created', { message: summaryOf(fresh), unreadTotal: 0 })
    await waitFor(() => expect(list.getAllByRole('listitem')).toHaveLength(2))
    expect(list.getAllByRole('listitem')[0]).toHaveTextContent('ci-bot@ci-bot')
  })
})

describe('Showing the Agent address', () => {
  it('copy address: the clipboard holds the full address', async () => {
    const { user } = await renderAgentInbox(received().slice(0, 1))
    await user.click(screen.getByRole('button', { name: 'Address' }))
    expect(await screen.findByText('test-org/test-project/Code review assistant', { selector: 'span.font-mono' })).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'CopyAddress' }))
    await expect(navigator.clipboard.readText()).resolves.toBe('test-org/test-project/Code review assistant')
    expect(await screen.findByRole('button', { name: 'CopyAddress' })).toHaveTextContent('Copied')
  })

  it("a platform Agent's address is its name itself", async () => {
    const watcher = makeAgent({ name: 'Integration patrol', scope: 'platform', projectId: null, address: 'Integration patrol' })
    const user = userEvent.setup()
    server.use(http.get(platformAgentsUrl, () => HttpResponse.json({ agents: [toListItem(watcher)] })), ...baseHandlers([watcher]))
    renderRoutes(routes, `/platform/agents/${watcher.id}/config`)
    await user.click(await screen.findByRole('button', { name: 'Address' }))
    expect(await screen.findByText('Integration patrol', { selector: 'span.font-mono' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'CopyAddress' })).toBeInTheDocument()
  })

  it('no address when the name does not meet the rules: a note says to rename it first and to use the internal ID when sending messages', async () => {
    const legacy = makeAgent({ name: 'old/bot', address: null })
    const user = userEvent.setup()
    server.use(...baseHandlers([legacy]))
    renderRoutes(routes, `${defaultAgentsPath}/${legacy.id}/config`)
    await user.click(await screen.findByRole('button', { name: 'Address' }))
    expect(await screen.findByText(/This Agent has no address yet.*Rename it to get one.*use its internal ID/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'CopyAddress' })).toBeNull()
  })
})

describe('The inbox on the sheet', () => {
  it('embeds counts directly inside the All and Unread only filter pills with no right-hand Mail stub', async () => {
    await renderAgentInbox(received())
    expect(screen.queryByRole('complementary', { name: 'Mail' })).toBeNull()
    const all = screen.getByRole('radio', { name: /^All/ })
    const unread = screen.getByRole('radio', { name: /^Unread only/ })
    for (const pill of [all, unread]) expect(pill).toHaveClass('h-[34px]', 'rounded-ticket', 'border', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:rounded-full')
    expect(all).toHaveAttribute('aria-checked', 'true')
    expect(all).toHaveClass(
      'data-[state=on]:bg-surface-active',
      'data-[state=on]:border-hairline-strong',
      '[[data-skin=plate]_&]:data-[state=on]:bg-riso-yellow',
      '[[data-skin=plate]_&]:data-[state=on]:border-ink',
      '[[data-skin=plate]_&]:data-[state=on]:shadow-[3px_3px_0_var(--ink)]',
    )
    expect(all.querySelector('[data-count="all"]')).toHaveTextContent(/^2$/)
    expect(unread.querySelector('[data-count="unread"]')).toHaveTextContent(/^1$/)
    expect(unread.querySelector('[data-count="unread"]')).toHaveClass('text-pink-text')

    const bar = all.closest('[data-inbox-bar]') as HTMLElement
    expect(bar).toHaveClass('border-b', 'border-hairline', '[[data-skin=plate]_&]:border-b-2', '[[data-skin=plate]_&]:border-ink')
    expect(within(bar).getByLabelText("Read-only view: opening a message doesn't mark it read")).toHaveTextContent('Read-only')
  })

  it('one row per message: sender with a monospace address, single-line body, monospace time; unread is a tilted stamp with a pink outline, read is a subtle pill', async () => {
    const [first, second] = received()
    const { list } = await renderAgentInbox([second, first])
    const [read, fresh] = list.getAllByRole('listitem')
    for (const item of [read, fresh]) expect(item).toHaveClass('border-b', 'border-hairline', '[[data-skin=plate]_&]:border-b-[1.5px]', '[[data-skin=plate]_&]:border-dashed')
    expect(within(fresh).getByText('test-org/test-project/Testing assistant')).toHaveClass('font-mono', 'truncate')
    expect(within(fresh).getByText('I added two tests for the naming package, give them a run.')).toHaveClass('truncate', 'font-semibold', '[[data-skin=plate]_&]:font-bold')
    expect(fresh.querySelector('time')).toHaveClass('font-mono')
    const stamp = within(fresh).getByText('Unread').closest('[data-read-state]')!
    expect(stamp).toHaveAttribute('data-read-state', 'unread')
    expect(stamp).toHaveClass('riso-tilt', 'text-pink-text')
    const pill = within(read).getByText(/^Read /).closest('[data-read-state]')!
    expect(pill).toHaveAttribute('data-read-state', 'read')
    expect(pill).toHaveClass('rounded-full', 'bg-surface-subtle', '[[data-skin=plate]_&]:bg-tint-blue')
    expect(within(read).getByText('Please review the naming package')).not.toHaveClass('font-bold')
  })

  it('wraps the message list in PageBody inside the sheet', async () => {
    const { list } = await renderAgentInbox(received())
    const area = list.getAllByRole('listitem')[0].closest('[data-page-body]')!
    expect(area).toHaveClass('overflow-y-auto')
    expect(area.closest('[data-sheet]')).toHaveClass('riso-scrollbar')
  })

  it('with unread only selected, the All pill still counts all messages', async () => {
    const [first, second] = received()
    const { user } = await renderAgentInbox([second, first])
    await user.click(screen.getByRole('radio', { name: /^Unread only/ }))
    const all = screen.getByRole('radio', { name: /^All/ })
    await waitFor(() => expect(all.querySelector('[data-count="all"]')).toHaveTextContent(/^2$/))
  })
})
