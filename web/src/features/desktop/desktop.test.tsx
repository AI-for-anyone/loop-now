import { act, render, screen, waitFor } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { apiUrl, defaultAgentsPath, defaultProject, makeAgent, projectsUrl } from '@/test/api'
import { agentParty, consoleParty, emitInbox, makeInboxMessage, openInboxStream, summaryOf } from '@/test/inbox'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { makeTaskDetail, nodeId, taskId } from '@/test/tasks'
import { BoardingSplash } from './BoardingSplash'
import type { DesktopBridge } from './desktop'

// Inside the desktop shell's window: inbox events are handed to the shell to show the Dock unread count and system notifications, and
// when a notification is clicked the shell hands the address back to the page; the top of the page leaves room for dragging the window.
const agent = makeAgent({ name: 'Reviewer' })
const question = (overrides: Partial<Schemas['InboxMessage']> = {}) =>
  summaryOf(
    makeInboxMessage({
      from: agentParty(agent),
      to: consoleParty,
      text: 'Merge this PR?',
      question: { options: [], expiresAt: '2026-12-01T00:00:00Z', state: 'open', answer: null },
      ...overrides,
    }),
  )

let bridge: { notify: ReturnType<typeof vi.fn>; setBadge: ReturnType<typeof vi.fn>; onNavigate: ReturnType<typeof vi.fn>; navigate?: (path: string) => void }
let focused = false

beforeEach(() => {
  bridge = { notify: vi.fn(), setBadge: vi.fn(), onNavigate: vi.fn() }
  bridge.onNavigate.mockImplementation((listener: (path: string) => void) => {
    bridge.navigate = listener
    return () => undefined
  })
  window.loopNowDesktop = bridge as unknown as DesktopBridge
  focused = false
  vi.spyOn(document, 'hasFocus').mockImplementation(() => focused)
})

afterEach(() => {
  delete window.loopNowDesktop
  vi.restoreAllMocks()
})

async function start() {
  server.use(...baseHandlers([agent]))
  renderRoutes(routes, defaultAgentsPath)
  await openInboxStream(2)
}

describe('Desktop shell notifications and the Dock unread count', () => {
  it('every inbox event updates the Dock unread count', async () => {
    await start()
    expect(bridge.setBadge).toHaveBeenLastCalledWith(2)
    await emitInbox('inbox.message.read', { id: crypto.randomUUID(), threadId: crypto.randomUUID(), to: consoleParty, readAt: '2026-11-03T00:00:00Z', unreadTotal: 1 })
    expect(bridge.setBadge).toHaveBeenLastCalledWith(1)
  })

  it('when the window has no focus, a new question an Agent sends you gives a "waiting for your reply" notification that opens its chat; the same message notifies only once', async () => {
    await start()
    const message = question()
    await emitInbox('inbox.message.created', { message, unreadTotal: 3 })
    await emitInbox('inbox.message.created', { message, unreadTotal: 3 })
    expect(bridge.notify).toHaveBeenCalledTimes(1)
    expect(bridge.notify).toHaveBeenCalledWith({ id: message.id, title: 'Waiting for your reply: Reviewer', body: 'Merge this PR?', path: `/agents/${agent.id}/chat` })
    expect(bridge.setBadge).toHaveBeenLastCalledWith(3)
  })

  it('an approval question says the Agent asks for approval', async () => {
    await start()
    const message = question({ text: 'git push origin main', question: { kind: 'permission', permissionRequestId: crypto.randomUUID(), options: [], expiresAt: '2026-12-01T00:00:00Z', state: 'open', answer: null } })
    await emitInbox('inbox.message.created', { message, unreadTotal: 1 })
    expect(bridge.notify).toHaveBeenCalledWith({ id: message.id, title: 'Reviewer asks for approval', body: 'git push origin main', path: `/agents/${agent.id}/chat` })
  })

  it('a message a task instance sends you gives a "task waiting" notification that opens the node of the task the message belongs to', async () => {
    const detail = makeTaskDetail(defaultProject.id, defaultProject.name)
    server.use(http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)), http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })))
    await start()
    const message = summaryOf(makeInboxMessage({ from: agentParty({ ...agent, name: 'task-checker' }), to: consoleParty, text: 'Check done, waiting for your confirmation', task: { taskId, nodeRunId: nodeId, runtimeId: crypto.randomUUID() } }))
    await emitInbox('inbox.message.created', { message, unreadTotal: 3 })
    await waitFor(() => expect(bridge.notify).toHaveBeenCalledTimes(1))
    expect(bridge.notify).toHaveBeenCalledWith({
      id: message.id,
      title: 'Task waiting: General task acceptance',
      body: 'task-checker: Check done, waiting for your confirmation',
      path: `/p/${encodeURIComponent(defaultProject.name)}/tasks/${taskId}?record=${nodeId}`,
    })
  })

  it('a question from a task instance is also "waiting for your reply", with the task name in the body', async () => {
    const detail = makeTaskDetail(defaultProject.id, defaultProject.name)
    server.use(http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)), http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })))
    await start()
    const message = question({ from: agentParty({ ...agent, name: 'task-checker' }), task: { taskId, nodeRunId: nodeId, runtimeId: crypto.randomUUID() } })
    await emitInbox('inbox.message.created', { message, unreadTotal: 3 })
    await waitFor(() => expect(bridge.notify).toHaveBeenCalledTimes(1))
    expect(bridge.notify.mock.calls[0][0]).toMatchObject({ title: 'Waiting for your reply: task-checker', body: 'Task "General task acceptance": Merge this PR?' })
  })

  it('no notification: while the window is visible and focused, for a plain message an Agent sends you, for a question no longer open, for a message sent to an Agent', async () => {
    await start()
    focused = true
    await emitInbox('inbox.message.created', { message: question(), unreadTotal: 3 })
    focused = false
    await emitInbox('inbox.message.created', { message: summaryOf(makeInboxMessage({ from: agentParty(agent), to: consoleParty, text: 'Done.' })), unreadTotal: 3 })
    await emitInbox('inbox.message.created', { message: question({ question: { options: [], expiresAt: '2026-12-01T00:00:00Z', state: 'answered', answer: null } }), unreadTotal: 3 })
    await emitInbox('inbox.message.created', { message: summaryOf(makeInboxMessage({ from: consoleParty, to: agentParty(agent), text: 'Go on.' })), unreadTotal: 3 })
    expect(bridge.notify).not.toHaveBeenCalled()
  })

  it('the window has no system title bar: the top bar of the sidebar, the main header bar and the narrow-screen top bar drag the window; none do when opened in a browser', async () => {
    server.use(...baseHandlers([agent]))
    const { unmount } = renderRoutes(routes, defaultAgentsPath)
    await openInboxStream(0)
    await screen.findByRole('heading', { name: 'Reviewer', level: 1 })
    const nav = screen.getByRole('navigation', { name: 'Main navigation' })
    expect(nav.firstElementChild).toHaveClass('window-drag', 'pl-[84px]')
    expect(screen.getByRole('button', { name: 'Open navigation' }).closest('header')).toHaveClass('window-drag')
    expect(document.querySelector('[data-agent-title-row]')).toHaveClass('window-drag')
    unmount()

    delete window.loopNowDesktop
    renderRoutes(routes, defaultAgentsPath)
    await openInboxStream(0)
    await screen.findByRole('heading', { name: 'Reviewer', level: 1 })
    expect(screen.getByRole('navigation', { name: 'Main navigation' }).firstElementChild).not.toHaveClass('window-drag')
    expect(screen.getByRole('button', { name: 'Open navigation' }).closest('header')).not.toHaveClass('window-drag')
    expect(document.querySelector('[data-agent-title-row]')).not.toHaveClass('window-drag')
    expect(document.querySelector('.window-drag')).toBeNull()
  })

  it('a clicked notification: the desktop shell hands the address back to the page and the console goes there', async () => {
    server.use(...baseHandlers([agent]))
    const { router } = renderRoutes(routes, defaultAgentsPath)
    await openInboxStream(0)
    expect(bridge.onNavigate).toHaveBeenCalled()
    bridge.navigate?.('/settings')
    await waitFor(() => expect(router.state.location.pathname).toBe('/settings'))
    expect(await screen.findByRole('heading', { level: 1 })).toBeInTheDocument()
  })
})

describe('Desktop boarding splash', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it('shows a logo-only boarding splash on desktop mount and replays it when the window is reopened, with no text', () => {
    vi.useFakeTimers()
    let reopen: (() => void) | undefined
    window.loopNowDesktop = {
      platform: 'darwin',
      notify: vi.fn(),
      setBadge: vi.fn(),
      onNavigate: () => () => undefined,
      onBoarding: (listener) => {
        reopen = listener
        return () => {
          reopen = undefined
        }
      },
    }

    render(<BoardingSplash />)
    const initial = screen.getByRole('status', { name: 'Loop Now' })
    expect(initial).toHaveAttribute('data-desktop-boarding', 'hold')
    expect(initial.textContent).toBe('')
    expect(initial.querySelector('.boarding-gold-square')).not.toBeNull()

    act(() => {
      vi.advanceTimersByTime(300)
    })
    expect(screen.getByRole('status', { name: 'Loop Now' })).toHaveAttribute('data-desktop-boarding', 'fade')

    act(() => {
      vi.advanceTimersByTime(240)
    })
    expect(screen.queryByRole('status', { name: 'Loop Now' })).toBeNull()

    // Reopening the desktop window replays the boarding splash and clears it cleanly.
    act(() => {
      reopen?.()
    })
    const reopened = screen.getByRole('status', { name: 'Loop Now' })
    expect(reopened).toHaveAttribute('data-desktop-boarding', 'hold')
    expect(reopened.textContent).toBe('')

    act(() => {
      vi.advanceTimersByTime(900)
    })
    expect(screen.getByRole('status', { name: 'Loop Now' })).toHaveAttribute('data-desktop-boarding', 'fade')

    act(() => {
      vi.advanceTimersByTime(250)
    })
    expect(screen.queryByRole('status', { name: 'Loop Now' })).toBeNull()
  })

  it('does not render in a regular browser', () => {
    delete window.loopNowDesktop
    const { container } = render(<BoardingSplash />)
    expect(container.firstChild).toBeNull()
  })
})
