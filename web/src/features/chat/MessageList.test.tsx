import type { ComponentProps, ReactElement, ReactNode } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render as rtlRender, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import type { MessagePages } from '@/api/queries'
import { makeAgent, makeMessage, makeMessagePage, makeRun } from '@/test/api'
import { createTestQueryClient } from '@/test/render'
import { initialLiveState, liveReducer, type LiveState } from './live-state'
import { resetChatDraftsForTest } from './draft-store'
import { MessageList } from './MessageList'
import { buildTurns } from './turns'

// The run record under a reply reads the query cache, so rendering needs a QueryClient.
function render(ui: ReactElement) {
  const client = createTestQueryClient()
  return rtlRender(ui, { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
}

// jsdom has no layout: the test sets the conversation area's content height and visible height, and triggers size changes itself.
const metrics = { scrollHeight: 0, clientHeight: 0 }

class FakeResizeObserver {
  static instances: FakeResizeObserver[] = []
  readonly targets = new Set<Element>()
  readonly callback: ResizeObserverCallback

  constructor(callback: ResizeObserverCallback) {
    this.callback = callback
    FakeResizeObserver.instances.push(this)
  }

  observe(target: Element) {
    this.targets.add(target)
  }

  unobserve(target: Element) {
    this.targets.delete(target)
  }

  disconnect() {
    this.targets.clear()
  }

  static resize(target: Element) {
    for (const observer of FakeResizeObserver.instances) {
      if (observer.targets.has(target)) observer.callback([], observer as unknown as ResizeObserver)
    }
  }
}

beforeEach(() => {
  FakeResizeObserver.instances = []
  vi.stubGlobal('ResizeObserver', FakeResizeObserver)
  metrics.scrollHeight = 1000
  metrics.clientHeight = 500
  for (const name of ['scrollHeight', 'clientHeight'] as const) {
    Object.defineProperty(HTMLElement.prototype, name, {
      configurable: true,
      get(this: HTMLElement) {
        return this.getAttribute('role') === 'log' ? metrics[name] : 0
      },
    })
  }
})

afterEach(() => {
  resetChatDraftsForTest()
  vi.unstubAllGlobals()
  Reflect.deleteProperty(HTMLElement.prototype, 'scrollHeight')
  Reflect.deleteProperty(HTMLElement.prototype, 'clientHeight')
  Reflect.deleteProperty(HTMLElement.prototype, 'scrollTop')
})

// Like a browser, clamp the conversation area's scrollTop between 0 and scrollHeight - clientHeight.
function clampScrollTop() {
  let top = 0
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get(this: HTMLElement) {
      return this.getAttribute('role') === 'log' ? top : 0
    },
    set(this: HTMLElement, value: number) {
      if (this.getAttribute('role') === 'log') top = Math.max(0, Math.min(value, metrics.scrollHeight - metrics.clientHeight))
    },
  })
}

function pagesOf(...texts: string[]): MessagePages {
  const messages = texts.flatMap((text, i) => {
    const run = makeRun({ startedAt: `2026-10-12T08:00:0${i}Z` })
    return [makeMessage({ text, run }), makeMessage({ role: 'assistant', text: `Answer to ${text}`, run })]
  })
  return { pages: [makeMessagePage(messages, { hasMore: true })], pageParams: [null] }
}

const agent = makeAgent()

// The chat page builds the conversation turns with buildTurns and hands them to MessageList
// (shared by the conversation stream and the queue list); build them the same way here.
function List({ pages, live, ...rest }: Omit<ComponentProps<typeof MessageList>, 'agent' | 'turns'> & { pages: MessagePages; live: LiveState }) {
  return <MessageList agent={agent} turns={buildTurns(pages, live).turns} {...rest} />
}

const props = { live: initialLiveState, hasOlder: true, loadingOlder: false, refreshing: false }

describe('conversation area scrolling', () => {
  // jsdom does no layout, so we can only confirm that the scroll area is a positioning container; that the whole
  // page does not get stretched taller is checked with browser screenshots (.local-build/chat-scroll-fix).
  it('the scroll area is a positioning container, so absolutely positioned notes inside it (unread dots and so on) do not stretch the page', () => {
    render(<List {...props} pages={pagesOf('Question')} onLoadOlder={() => {}} />)
    expect(screen.getByRole('log')).toHaveClass('relative')
  })

  it('when resting at the bottom it stays stuck to the bottom as the content grows (an image finishes loading, tool markers wrap)', () => {
    render(<List {...props} pages={pagesOf('Question')} onLoadOlder={() => {}} />)
    const log = screen.getByRole('log')
    expect(log.scrollTop).toBe(1000)
    metrics.scrollHeight = 1300
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    expect(log.scrollTop).toBe(1300)
  })

  it('when the content grows a lot right after a programmatic stick to the bottom, a late scroll event does not cancel sticking', () => {
    clampScrollTop()
    render(<List {...props} pages={pagesOf('Question')} onLoadOlder={() => {}} />)
    const log = screen.getByRole('log')
    expect(log.scrollTop).toBe(500)
    // When the reply ends, the tool markers wrap and the final text appears, so the content grows by
    // 300 at once; only then does the scroll event caused by the programmatic stick-to-bottom arrive.
    metrics.scrollHeight = 1300
    fireEvent.scroll(log)
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    expect(log.scrollTop).toBe(800)
  })

  it('after a person scrolls up away from the bottom it stops sticking, and sticks again after scrolling back near the bottom', () => {
    clampScrollTop()
    render(<List {...props} pages={pagesOf('Question')} onLoadOlder={() => {}} />)
    const log = screen.getByRole('log')
    log.scrollTop = 300
    fireEvent.scroll(log)
    metrics.scrollHeight = 1300
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    expect(log.scrollTop).toBe(300)
    log.scrollTop = 760
    fireEvent.scroll(log)
    metrics.scrollHeight = 1600
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    expect(log.scrollTop).toBe(1100)
  })

  it('away from the bottom, growing content does not change the position', () => {
    render(<List {...props} pages={pagesOf('Question')} onLoadOlder={() => {}} />)
    const log = screen.getByRole('log')
    log.scrollTop = 200
    fireEvent.scroll(log)
    metrics.scrollHeight = 1300
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    expect(log.scrollTop).toBe(200)
  })

  it('after loading earlier messages compensates by the content height at the start of loading, keeping the view in place', () => {
    const onLoadOlder = vi.fn()
    const newer = pagesOf('New question')
    const { rerender } = render(<List {...props} pages={newer} onLoadOlder={onLoadOlder} />)
    const log = screen.getByRole('log')
    log.scrollTop = 300
    fireEvent.scroll(log)
    // An image in view finishes loading and the content gets taller, but there is no new message.
    metrics.scrollHeight = 1200
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    log.scrollTop = 0
    fireEvent.scroll(log)
    expect(onLoadOlder).toHaveBeenCalledTimes(1)
    rerender(<List {...props} loadingOlder pages={newer} onLoadOlder={onLoadOlder} />)
    // An older page is inserted at the top, adding 500 to the content.
    const older = pagesOf('Old question')
    metrics.scrollHeight = 1700
    rerender(<List {...props} pages={{ pages: [...newer.pages, ...older.pages], pageParams: [null, 'x'] }} onLoadOlder={onLoadOlder} />)
    expect(screen.getByText('Old question')).toBeInTheDocument()
    expect(log.scrollTop).toBe(500)
  })

  it('does not load earlier messages while the whole page is being refetched', () => {
    const onLoadOlder = vi.fn()
    render(<List {...props} refreshing pages={pagesOf('Question')} onLoadOlder={onLoadOlder} />)
    const log = screen.getByRole('log')
    log.scrollTop = 0
    fireEvent.scroll(log)
    fireEvent.click(screen.getByRole('button', { name: 'Load earlier messages' }))
    expect(onLoadOlder).not.toHaveBeenCalled()
  })

  it('shows a floating Back to latest button when scrolled up, switches to New output when streaming grows, and clicking it jumps to bottom and resumes auto-follow', () => {
    clampScrollTop()
    const run = makeRun({ status: 'running', endedAt: null })
    const pages: MessagePages = { pages: [makeMessagePage([makeMessage({ text: 'Question', run })])], pageParams: [null] }
    const { rerender } = render(<List {...props} live={{ ...initialLiveState, current: { run, text: 'Part 1' } }} pages={pages} onLoadOlder={() => {}} />)
    const log = screen.getByRole('log')
    expect(screen.queryByRole('button', { name: 'Back to latest' })).toBeNull()
    expect(log).toHaveClass('riso-scrollbar')
    expect(log).not.toHaveClass('nav:no-scrollbar')

    log.scrollTop = 180
    fireEvent.scroll(log)
    expect(screen.getByRole('button', { name: 'Back to latest' })).toBeInTheDocument()

    // New streaming output arrives while scrolled up: the floating button switches to "New output".
    metrics.scrollHeight = 1400
    rerender(<List {...props} live={{ ...initialLiveState, current: { run, text: 'Part 1 and Part 2' } }} pages={pages} onLoadOlder={() => {}} />)
    const newOutput = screen.getByRole('button', { name: 'New output' })
    expect(newOutput).toBeInTheDocument()
    expect(log.scrollTop).toBe(180)

    // Clicking the floating button jumps to bottom, clears the button, and resumes auto-follow.
    fireEvent.click(newOutput)
    expect(log.scrollTop).toBe(900)
    expect(screen.queryByRole('button', { name: 'New output' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Back to latest' })).toBeNull()
    metrics.scrollHeight = 1700
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    expect(log.scrollTop).toBe(1200)
  })

  it('restores a saved non-bottom scroll position when remounting the same Agent', () => {
    clampScrollTop()
    const first = render(<List {...props} pages={pagesOf('First', 'Second')} onLoadOlder={() => {}} />)
    const log = screen.getByRole('log')
    log.scrollTop = 220
    fireEvent.scroll(log)
    first.unmount()

    render(<List {...props} pages={pagesOf('First', 'Second')} onLoadOlder={() => {}} />)
    expect(screen.getByRole('log').scrollTop).toBe(220)
    expect(screen.getByRole('button', { name: 'Back to latest' })).toBeInTheDocument()
  })
})

describe('how replies show', () => {
  it('while replying shows the output cursor and static status text; after it ends the cursor is gone', () => {
    const run = makeRun({ status: 'running', endedAt: null })
    const live: LiveState = { ...initialLiveState, current: { run, text: 'Half written' } }
    const history = (messages: ReturnType<typeof makeMessage>[]): MessagePages => ({ pages: [makeMessagePage(messages)], pageParams: [null] })
    const { container, rerender } = render(<List {...props} live={live} pages={history([makeMessage({ text: 'Question', run })])} onLoadOlder={() => {}} />)
    expect(screen.getByText('Half written')).toBeInTheDocument()
    expect(screen.getByText('Replying')).toBeInTheDocument()
    expect(container.querySelector('.riso-cursor')?.className).toContain('motion-reduce:animate-none')
    expect(container.querySelector('[class*="animate-spin"]')).toBeNull()

    const ended = { ...run, status: 'completed' as const, endedAt: '2026-10-12T08:00:30Z' }
    rerender(
      <List
        {...props}
        pages={history([makeMessage({ text: 'Question', run: ended }), makeMessage({ role: 'assistant', text: 'All written', run: ended })])}
        onLoadOlder={() => {}}
      />,
    )
    expect(screen.getByText('All written')).toBeInTheDocument()
    expect(screen.queryByText('Replying')).toBeNull()
    expect(container.querySelector('.riso-cursor')).toBeNull()
  })

  it('a /pi: extension command handled by Pi itself (piCommand.handled) with no output: the user message shows as typed, the reply says the command ran with no reply; commands expanded into a prompt show their reply as usual', () => {
    const handled = makeRun({ startedAt: '2026-10-12T08:00:00Z', piCommand: { name: 'review', handled: true, notices: [], truncated: false } })
    const expanded = makeRun({ startedAt: '2026-10-12T08:00:01Z', piCommand: { name: 'skill:triage', handled: false, notices: [], truncated: false } })
    const plain = makeRun({ startedAt: '2026-10-12T08:00:02Z' })
    render(
      <List
        {...props}
        pages={{
          pages: [makeMessagePage([
            makeMessage({ text: '/pi:review src/', run: handled }), makeMessage({ role: 'assistant', text: '', run: handled }),
            makeMessage({ text: '/pi:skill:triage take a look', run: expanded }), makeMessage({ role: 'assistant', text: 'Sorted into categories', run: expanded }),
            makeMessage({ text: 'A plain question', run: plain }), makeMessage({ role: 'assistant', text: '', run: plain }),
          ])],
          pageParams: [null],
        }}
        onLoadOlder={() => {}}
      />,
    )
    expect(screen.getByText('/pi:review src/')).toBeInTheDocument()
    expect(screen.getByText('/pi:skill:triage take a look')).toBeInTheDocument()
    expect(screen.getByText('Sorted into categories')).toBeInTheDocument()
    expect(screen.getAllByText('Command ran; no reply')).toHaveLength(1)
  })

  it('extension command output (notices) shows in order as monospace plain text, keeping line breaks and spaces; warnings and errors name their level; truncation is noted; with output it does not say "no reply"', () => {
    const usage = 'Weekly Limit (SuperGrok)\n   Used       1%\n   Reset      Nov 14, 04:41 EST'
    const run = makeRun({
      startedAt: '2026-10-12T08:00:00Z',
      piCommand: { name: 'grok-cli-usage', handled: true, truncated: true, notices: [{ level: 'info', message: usage }, { level: 'warning', message: 'Quota almost used up <b>careful</b>' }, { level: 'error', message: 'Failed to read the monthly quota' }] },
    })
    const { container } = render(
      <List
        {...props}
        pages={{ pages: [makeMessagePage([makeMessage({ text: '/pi:grok-cli-usage', run }), makeMessage({ role: 'assistant', text: '', run })])], pageParams: [null] }}
        onLoadOlder={() => {}}
      />,
    )
    const blocks = [...container.querySelectorAll('[data-pi-command-notices] pre')]
    expect(blocks.map((block) => block.textContent)).toEqual([usage, 'Quota almost used up <b>careful</b>', 'Failed to read the monthly quota'])
    expect(blocks[0].className).toContain('whitespace-pre-wrap')
    expect(blocks[0].className).toContain('font-mono')
    expect(blocks[1].parentElement).toHaveTextContent(/^Warning/)
    expect(blocks[2].parentElement).toHaveTextContent(/^Error/)
    expect(blocks[0].parentElement).not.toHaveTextContent(/^(Warning|Error)/)
    expect(container.querySelector('[data-pi-command-notices] b')).toBeNull()
    expect(screen.getByText('The output was too long and has been truncated')).toBeInTheDocument()
    expect(screen.queryByText('Command ran; no reply')).toBeNull()
  })

  it('no output while running; the notices brought by the end event show at once, without fetching the timeline', () => {
    const running = makeRun({ status: 'running', endedAt: null, piCommand: { name: 'grok-cli-usage', handled: true, notices: [], truncated: false } })
    const user = makeMessage({ text: '/pi:grok-cli-usage', run: running })
    const history: MessagePages = { pages: [makeMessagePage([user])], pageParams: [null] }
    const live: LiveState = { ...initialLiveState, current: { run: running, text: '' } }
    const { container, rerender } = render(<List {...props} live={live} pages={history} onLoadOlder={() => {}} />)
    expect(container.querySelector('[data-pi-command-notices]')).toBeNull()
    expect(screen.queryByText('Command ran; no reply')).toBeNull()
    const ended = { ...running, status: 'completed' as const, endedAt: '2026-10-12T08:00:30Z', piCommand: { name: 'grok-cli-usage', handled: true, notices: [{ level: 'info' as const, message: 'Used 1%' }], truncated: false } }
    const next = liveReducer(live, { type: 'event', event: { type: 'run.ended', id: '5-0', data: { run: ended, message: makeMessage({ role: 'assistant', text: '', run: ended }) } } })
    rerender(<List {...props} live={next} pages={history} onLoadOlder={() => {}} />)
    expect(container.querySelector('[data-pi-command-notices] pre')?.textContent).toBe('Used 1%')
  })

  it('an interrupt message is marked “Interrupt” above the user message', () => {
    const run = makeRun({ whenBusy: 'interrupt' })
    render(
      <List
        {...props}
        pages={{ pages: [makeMessagePage([makeMessage({ text: 'Stop first', run }), makeMessage({ role: 'assistant', text: 'OK', run })])], pageParams: [null] }}
        onLoadOlder={() => {}}
      />,
    )
    expect(screen.getByText('Interrupt')).toBeInTheDocument()
  })

  it('the interrupted turn: unfinished tools turn gray marked interrupted, followed by an “Interrupted; handling this first” divider, and the interrupt message bubble is pink', () => {
    const interrupted = makeRun({
      status: 'interrupted',
      endReason: 'user_interrupt',
      startedAt: '2026-10-12T08:00:00Z',
      endedAt: '2026-10-12T08:01:00Z',
      tools: [
        { toolCallId: 'read-1', toolName: 'read', state: 'started', at: '2026-10-12T08:00:01Z' },
        { toolCallId: 'read-1', toolName: 'read', state: 'succeeded', at: '2026-10-12T08:00:02Z' },
        { toolCallId: 'bash-1', toolName: 'bash', state: 'started', at: '2026-10-12T08:00:03Z' },
      ],
    })
    const interrupt = makeRun({ whenBusy: 'interrupt', startedAt: '2026-10-12T08:01:01Z' })
    const later = makeRun({ whenBusy: 'interrupt', startedAt: '2026-10-12T08:02:00Z' })
    const messages = [
      makeMessage({ text: 'Run the tests', run: interrupted }),
      makeMessage({ role: 'assistant', text: 'Running them once', run: interrupted }),
      makeMessage({ text: 'Run lint first', run: interrupt }),
      makeMessage({ role: 'assistant', text: 'Sure', run: interrupt }),
      makeMessage({ text: 'The previous turn ended normally', run: later }),
    ]
    const { container } = render(<List {...props} pages={{ pages: [makeMessagePage(messages)], pageParams: [null] }} onLoadOlder={() => {}} />)
    const tools = within(screen.getAllByRole('list', { name: 'Tool calls' })[0]).getAllByRole('listitem')
    expect(tools[0]).toHaveTextContent('readSucceeded')
    expect(tools[1]).toHaveTextContent('bashInterrupted')
    expect(tools[1].querySelector('[data-interrupted]')).toHaveClass('border-dashed')
    expect(container.querySelector('[data-slot="tool-spinner"]')).toBeNull()
    // It appears only once, after the turn that was interrupted; there is
    // none before an interrupting message whose previous turn ended normally.
    const dividers = screen.getAllByRole('separator', { name: 'Interrupted; handling this first' })
    expect(dividers).toHaveLength(1)
    const bubble = screen.getByText('Run lint first')
    expect(dividers[0].compareDocumentPosition(bubble) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(bubble).toHaveClass('bg-tint-pink-2', '[[data-skin=plate]_&]:border-riso-pink')
    expect(screen.getByText('Run the tests')).toHaveClass('bg-surface-subtle', '[[data-skin=plate]_&]:bg-tint-pink')
  })

  // A runtime may never report a tool's end (Antigravity moves a long command to the background and its end arrives after the turn). The
  // server now closes such a tool as no_result when the turn ends; records saved before that still have it on started.
  const unfinishedTools: Schemas['ToolEvent'][] = [
    { toolCallId: 'read-1', toolName: 'read', state: 'started', at: '2026-10-12T08:00:01Z' },
    { toolCallId: 'read-1', toolName: 'read', state: 'succeeded', at: '2026-10-12T08:00:02Z' },
    { toolCallId: 'exec-1', toolName: 'execute', state: 'started', at: '2026-10-12T08:00:03Z' },
    { toolCallId: 'exec-2', toolName: 'execute', state: 'failed', at: '2026-10-12T08:00:04Z' },
    { toolCallId: 'exec-3', toolName: 'execute', state: 'started', at: '2026-10-12T08:00:05Z' },
    { toolCallId: 'exec-3', toolName: 'execute', state: 'no_result', at: '2026-10-12T08:01:35Z' },
  ]
  const toolRows = () => within(screen.getByRole('list', { name: 'Tool calls' })).getAllByRole('listitem')

  it.each([
    ['completed', { status: 'completed' }],
    ['failed', { status: 'failed', errorCode: 'runtime_error', errorMessage: 'boom' }],
    ['stopped', { status: 'interrupted', endReason: 'user_stopped' }],
    ['cut off by a server restart', { status: 'interrupted', endReason: 'server_restart' }],
  ] as const)('once the turn has ended (%s), a tool closed as no_result, or an old record still on started, shows a neutral “No result”', (_how, fields) => {
    const run = makeRun({ ...fields, startedAt: '2026-10-12T08:00:00Z', endedAt: '2026-10-12T08:01:35Z', tools: unfinishedTools })
    const { container } = render(<List {...props} pages={{ pages: [makeMessagePage([makeMessage({ text: 'Run the tests', run }), makeMessage({ role: 'assistant', text: 'Done', run })])], pageParams: [null] }} onLoadOlder={() => {}} />)
    const [read, old, failed, closed] = toolRows()
    expect(read).toHaveTextContent('readSucceeded')
    expect(old).toHaveTextContent('executeNo result')
    expect(failed).toHaveTextContent('executeFailed')
    expect(closed).toHaveTextContent('executeNo result')
    expect(container.querySelector('[data-slot="tool-spinner"]')).toBeNull()
    // Told apart from success and failure, and from tools a person's interrupt cut off.
    for (const row of [old, closed]) {
      const mark = row.querySelector('[data-slot="badge"]')
      expect(mark).toHaveAttribute('data-no-result')
      expect(mark?.className).not.toMatch(/tint-green|tint-red|border-dashed/)
    }
  })

  it('while the turn is still going, a tool without its end event shows running with a spinner', () => {
    // The server closes tools as no_result only when the turn ends, so a running turn has none.
    const run = makeRun({ status: 'running', startedAt: '2026-10-12T08:00:00Z', runningAt: '2026-10-12T08:00:00Z', endedAt: null, tools: unfinishedTools.filter((tool) => tool.toolCallId !== 'exec-3') })
    const { container } = render(<List {...props} pages={{ pages: [makeMessagePage([makeMessage({ text: 'Run the tests', run })])], pageParams: [null] }} onLoadOlder={() => {}} />)
    expect(toolRows()[1]).toHaveTextContent('executeRunning')
    expect(screen.queryByText('No result')).toBeNull()
    expect(container.querySelectorAll('[data-slot="tool-spinner"]')).toHaveLength(1)
  })

  it('the session divider is a pink dashed cut line with scissors, the same style as the “interrupt” divider', () => {
    const before = makeRun({ status: 'interrupted', endReason: 'user_interrupt', startedAt: '2026-10-12T08:00:00Z', endedAt: '2026-10-12T08:01:00Z' })
    const interrupt = makeRun({ whenBusy: 'interrupt', startedAt: '2026-10-12T08:01:01Z' })
    const reopened = makeRun({ startedAt: '2026-10-12T09:00:00Z' })
    const messages = [
      makeMessage({ text: 'First question', run: before }),
      makeMessage({ text: 'Do this first', run: interrupt }),
      makeMessage({ text: 'Question after the new session', run: reopened, sessionEpoch: 2 }),
    ]
    render(<List {...props} pages={{ pages: [makeMessagePage(messages)], pageParams: [null] }} onLoadOlder={() => {}} />)
    const session = screen.getByRole('separator', { name: 'Session switched; page history is kept' })
    const interrupted = screen.getByRole('separator', { name: 'Interrupted; handling this first' })
    expect(session).toHaveTextContent(/^Session switched; page history is kept$/)
    for (const divider of [session, interrupted]) {
      const lines = divider.querySelectorAll('[data-cut-line]')
      expect(lines).toHaveLength(2)
      for (const line of lines) expect(line).toHaveClass('border-t', 'border-hairline', '[[data-skin=plate]_&]:border-t-2', '[[data-skin=plate]_&]:border-dashed', '[[data-skin=plate]_&]:border-riso-pink')
      expect(divider.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    }
    // The cut line has a pair of scissors at its left end; the interrupt divider's icon is still a lightning bolt.
    expect(session.querySelector('svg')).toHaveClass('lucide-scissors')
    expect(interrupted.querySelector('svg')).toHaveClass('lucide-zap')
    expect(session.compareDocumentPosition(screen.getByText('Question after the new session')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('a turn woken by the Inbox: the sender is marked as a platform reminder, distinct from messages the operator sent, followed by the reply', () => {
    const run = makeRun()
    const reminder = makeMessage({ origin: 'inbox_reminder', text: 'You have 2 unread Inbox messages. Run loopctl inbox list --unread to see them.', run })
    render(
      <List
        {...props}
        pages={{ pages: [makeMessagePage([reminder, makeMessage({ role: 'assistant', text: 'I will take a look', run })])], pageParams: [null] }}
        onLoadOlder={() => {}}
      />,
    )
    const note = screen.getByRole('note')
    expect(note).toHaveTextContent(/^Platform reminder.+Received an Inbox message/)
    expect(note).not.toHaveTextContent('loopctl')
    expect(note).not.toHaveTextContent('You have 2 unread')
    expect(screen.queryByText(/^You\s*·/)).toBeNull()
    expect(screen.getByText('I will take a look')).toBeInTheDocument()
  })
})

it('sleep_wake is one faint wake line: time, how long it slept and the note; the platform instruction and the sleep ID are not in the text; a message that woke it early only gets a small marker', () => {
  const timer = '00000000-0000-7000-8000-000000005001', early = '00000000-0000-7000-8000-000000005002'
  const source = makeRun({ endedAt: '2026-10-26T08:00:00Z' }), wake = makeRun({ wakeSleepId: timer, endedAt: '2026-10-26T08:05:20Z' }), consoleRun = makeRun({ wakeSleepId: early })
  const pages: MessagePages = { pages: [makeMessagePage([
    makeMessage({ text: 'Check CI every 5 minutes', run: source, createdAt: '2026-10-26T07:59:50Z' }),
    makeMessage({ role: 'assistant', text: 'Not done yet, sleeping first', run: source, createdAt: '2026-10-26T07:59:55Z' }),
    makeMessage({ origin: 'sleep_wake', text: `Platform notice: your sleep is due; continue in the original conversation. sleepId=${timer}; note: check CI status for the budget fixture\nthen report`, run: wake, createdAt: '2026-10-26T08:05:03Z' }),
    makeMessage({ role: 'assistant', text: 'Continued', run: wake, createdAt: '2026-10-26T08:05:04Z' }),
    makeMessage({ origin: 'console', text: 'A real console message', run: consoleRun, createdAt: '2026-10-26T08:06:00Z' }),
  ])], pageParams: [null] }
  render(<List {...props} pages={pages} onLoadOlder={() => {}} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(/^Woke up on schedule·.+·Slept 5 min·check CI status for the budget fixture then report$/u)
  expect(line).not.toHaveTextContent(/Platform notice|sleepId|5001/u)
  expect(line).toHaveAttribute('title', `check CI status for the budget fixture then report\nSleep ${timer}`)
  expect(line.className).not.toMatch(/riso-tilt|riso-yellow/)
  expect(screen.getByText('A real console message')).toBeVisible()
  expect(screen.getByText('Woke early').closest('[data-early-wake]')).toHaveAttribute('title', `Sleep ${early}`)
  expect(screen.queryByText(/Wake ref/u)).toBeNull()
})

it('does not say how long it slept when the previous turn is not the one that just ended; a wake text that does not match the format goes only into the hover tooltip', () => {
  const wake = makeRun({ wakeSleepId: 'sleep-timer' })
  const pages: MessagePages = { pages: [makeMessagePage([
    makeMessage({ origin: 'sleep_wake', text: 'Raw timer reminder', run: wake }),
    makeMessage({ role: 'assistant', text: 'Continued', run: wake }),
  ])], pageParams: [null] }
  render(<List {...props} pages={pages} onLoadOlder={() => {}} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(/^Woke up on schedule·[^·]+$/u)
  expect(line).toHaveAttribute('title', 'Raw timer reminder\nSleep sleep-timer')
})
