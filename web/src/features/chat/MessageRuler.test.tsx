import type { ComponentProps, ReactElement, ReactNode } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render as rtlRender, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import type { MessagePages } from '@/api/queries'
import { makeAgent, makeMessage, makeMessagePage, makeRun } from '@/test/api'
import { FakeIntersectionObserver } from '@/test/intersection-observer'
import { createTestQueryClient } from '@/test/render'
import { initialLiveState } from './live-state'
import { resetChatDraftsForTest } from './draft-store'
import { MessageList } from './MessageList'
import { turnPreview } from './ruler'
import { buildTurns, type Turn } from './turns'

// The run record under a reply reads the query cache, so rendering needs a QueryClient.
function render(ui: ReactElement) {
  const client = createTestQueryClient()
  return rtlRender(ui, { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
}

// jsdom has no layout: the test sets the sizes of the conversation area and the ruler, and
// clamps the conversation area's scrollTop to the scrollable range like a browser does.
const log = { scrollHeight: 2000, clientHeight: 500, top: 0 }
const ruler = { scrollHeight: 0, clientHeight: 0 }

beforeEach(() => {
  log.scrollHeight = 2000
  log.clientHeight = 500
  log.top = 0
  ruler.scrollHeight = 0
  ruler.clientHeight = 0
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  )
  for (const name of ['scrollHeight', 'clientHeight'] as const) {
    Object.defineProperty(HTMLElement.prototype, name, {
      configurable: true,
      get(this: HTMLElement) {
        if (this.getAttribute('role') === 'log') return log[name]
        return this.tagName === 'OL' ? ruler[name] : 0
      },
    })
  }
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get(this: HTMLElement) {
      return this.getAttribute('role') === 'log' ? log.top : Number(this.dataset.testScrollTop ?? 0)
    },
    set(this: HTMLElement, value: number) {
      if (this.getAttribute('role') === 'log') log.top = Math.max(0, Math.min(value, log.scrollHeight - log.clientHeight))
      else this.dataset.testScrollTop = String(value)
    },
  })
})

afterEach(() => {
  resetChatDraftsForTest()
  vi.unstubAllGlobals()
  for (const name of ['scrollHeight', 'clientHeight', 'scrollTop', 'offsetTop', 'offsetHeight']) Reflect.deleteProperty(HTMLElement.prototype, name)
})

function pagesOf(texts: string[], hasMore = true): MessagePages {
  const messages = texts.flatMap((text, i) => {
    const run = makeRun({ startedAt: new Date(Date.parse('2026-11-12T08:00:00Z') + i * 1000).toISOString() })
    return [makeMessage({ text, run }), makeMessage({ role: 'assistant', text: `Answer to ${text}`, run })]
  })
  return { pages: [makeMessagePage(messages, { hasMore })], pageParams: [null] }
}

const agent = makeAgent()
const props = { hasOlder: true, loadingOlder: false, refreshing: false, onLoadOlder: () => {} }

function List({ pages, ...rest }: Omit<ComponentProps<typeof MessageList>, 'agent' | 'turns' | 'hasOlder' | 'loadingOlder' | 'refreshing' | 'onLoadOlder'> &
  Partial<ComponentProps<typeof MessageList>> & { pages: MessagePages }) {
  return <MessageList agent={agent} turns={buildTurns(pages, initialLiveState).turns} {...props} {...rest} />
}

const nav = () => screen.getByRole('navigation', { name: 'Conversation turns' })
const ticks = () => within(nav().querySelector('ol')!).getAllByRole('button')
const turnBlock = (text: string) => screen.getByText(text).closest<HTMLElement>('[data-run-id]')!
const rangeMarks = () => [...nav().querySelectorAll<HTMLElement>('[data-ruler-range]')]
const majorTicks = () => ticks().filter((tick) => tick.querySelector('[data-ruler-tick][data-major="true"]'))
const total = () => nav().querySelector<HTMLElement>('[data-ruler-total]')
const texts = (count: number) => Array.from({ length: count }, (_, i) => `Question ${i + 1}`)

describe('ruler on the right of the conversation area', () => {
  it('one tick per turn (turns that have started), named by the start of the user message; queued messages do not count; ticks are added after loading earlier messages', () => {
    const pages = pagesOf(['First question', 'Second question', 'Third question'])
    const queuedRun = makeRun({ status: 'queued', startedAt: null, runningAt: null, endedAt: null })
    pages.pages[0].messages.push(makeMessage({ text: 'Queued question', run: queuedRun }))
    const { rerender } = render(<List pages={pages} />)
    expect(ticks().map((tick) => tick.getAttribute('aria-label'))).toEqual(['First question', 'Second question', 'Third question'])

    const older = pagesOf(['Earlier one', 'Earlier two'])
    for (const [i, message] of older.pages[0].messages.entries()) message.run.startedAt = new Date(Date.parse('2026-11-11T08:00:00Z') + i).toISOString()
    rerender(<List pages={{ pages: [...pages.pages, ...older.pages], pageParams: [null, 'x'] }} />)
    expect(ticks().map((tick) => tick.getAttribute('aria-label'))).toEqual(['Earlier one', 'Earlier two', 'First question', 'Second question', 'Third question'])
  })

  it('not shown with only one turn or no conversation', () => {
    const { rerender } = render(<List pages={pagesOf(['The only question'])} />)
    expect(screen.queryByRole('navigation')).toBeNull()
    rerender(<List pages={pagesOf([])} />)
    expect(screen.queryByRole('navigation')).toBeNull()
    rerender(<List pages={pagesOf(['One', 'Two'])} />)
    expect(ticks()).toHaveLength(2)
  })

  it('hidden on narrow screens (when the sidebar folds into a drawer)', () => {
    render(<List pages={pagesOf(['One', 'Two'])} />)
    expect(nav()).toHaveClass('max-nav:hidden')
  })

  it('ticks of the turns visible in the conversation area are marked current with a contiguous range spine; they follow scrolling', () => {
    render(<List pages={pagesOf(['One', 'Two', 'Three', 'Four'])} />)
    act(() => {
      FakeIntersectionObserver.trigger(turnBlock('Three'), true)
      FakeIntersectionObserver.trigger(turnBlock('Four'), true)
    })
    const current = () => ticks().filter((tick) => tick.getAttribute('aria-current') === 'true').map((tick) => tick.getAttribute('aria-label'))
    expect(current()).toEqual(['Three', 'Four'])
    expect(rangeMarks()).toHaveLength(1)
    expect(rangeMarks()[0].closest('li')).toContainElement(within(nav()).getByRole('button', { name: 'Three' }))
    expect(rangeMarks()[0]).toHaveAttribute('data-count', '2')
    act(() => {
      FakeIntersectionObserver.trigger(turnBlock('Four'), false)
      FakeIntersectionObserver.trigger(turnBlock('Two'), true)
    })
    expect(current()).toEqual(['Two', 'Three'])
    expect(rangeMarks()).toHaveLength(1)
    expect(rangeMarks()[0].closest('li')).toContainElement(within(nav()).getByRole('button', { name: 'Two' }))
    expect(rangeMarks()[0]).toHaveAttribute('data-count', '2')
  })

  it('clicking a tick jumps to the start of that turn; content growing afterwards does not pull back to the bottom, and the scroll from the jump does not load earlier messages', async () => {
    const onLoadOlder = vi.fn()
    const pages = pagesOf(['One', 'Two', 'Three'])
    const { rerender } = render(<List pages={pages} onLoadOlder={onLoadOlder} />)
    const el = screen.getByRole('log')
    expect(el.scrollTop).toBe(1500)
    // The start of the first turn is -1420px below the top of the conversation area (far above the view).
    el.getBoundingClientRect = () => DOMRect.fromRect({ x: 0, y: 100, width: 600, height: 500 })
    turnBlock('One').getBoundingClientRect = () => DOMRect.fromRect({ x: 0, y: 100 - 1420, width: 600, height: 300 })
    await userEvent.setup().click(within(nav()).getByRole('button', { name: 'One' }))
    expect(el.scrollTop).toBe(1500 - 1420 - 24)
    fireEvent.scroll(el)
    expect(onLoadOlder).not.toHaveBeenCalled()
    // The last turn's reply grows longer and the content gets taller: do not stick to the bottom.
    log.scrollHeight = 2400
    const messages = pages.pages[0].messages
    rerender(<List pages={{ ...pages, pages: [{ ...pages.pages[0], messages: [...messages.slice(0, -1), { ...messages.at(-1)!, text: 'Answer to Three, now longer' }] }] }} onLoadOlder={onLoadOlder} />)
    expect(screen.getByText('Answer to Three, now longer')).toBeInTheDocument()
    expect(el.scrollTop).toBe(56)
    // When the person later scrolls near the top themselves, older messages load as usual.
    el.scrollTop = 20
    fireEvent.scroll(el)
    expect(onLoadOlder).toHaveBeenCalledTimes(1)
  })

  it('in locate mode jumping to the last turn near the bottom does not load newer messages; Enter jumps too', async () => {
    const onLoadNewer = vi.fn()
    const pages = pagesOf(['One', 'Two', 'Three'])
    render(<List pages={pages} targetRunId={pages.pages[0].messages[0].run.id} hasNewer onLoadNewer={onLoadNewer} />)
    const el = screen.getByRole('log')
    el.getBoundingClientRect = () => DOMRect.fromRect({ x: 0, y: 0, width: 600, height: 500 })
    turnBlock('Three').getBoundingClientRect = () => DOMRect.fromRect({ x: 0, y: 1800, width: 600, height: 200 })
    const user = userEvent.setup()
    within(nav()).getByRole('button', { name: 'Three' }).focus()
    await user.keyboard('{Enter}')
    expect(el.scrollTop).toBe(1500)
    fireEvent.scroll(el)
    expect(onLoadNewer).not.toHaveBeenCalled()
  })

  it('hovering or keyboard-focusing a tick shows the start of that turn on the left, and it goes away when moving off', async () => {
    render(<List pages={pagesOf(['Help me see why the build failed', 'Run the tests again'])} />)
    const user = userEvent.setup()
    const preview = () => nav().querySelector('[data-ruler-preview]')
    await user.hover(within(nav()).getByRole('button', { name: 'Help me see why the build failed' }))
    expect(preview()).toHaveTextContent('Help me see why the build failed')
    expect(preview()).toHaveAttribute('aria-hidden', 'true')
    await user.unhover(within(nav()).getByRole('button', { name: 'Help me see why the build failed' }))
    expect(preview()).toBeNull()
    act(() => within(nav()).getByRole('button', { name: 'Help me see why the build failed' }).focus())
    expect(preview()).toHaveTextContent('Help me see why the build failed')
    await user.tab()
    expect(within(nav()).getByRole('button', { name: 'Run the tests again' })).toHaveFocus()
    expect(preview()).toHaveTextContent('Run the tests again')
    await user.tab()
    expect(preview()).toBeNull()
    // A mouse click also focuses the tick, but the preview does not stay once the mouse moves away.
    await user.click(within(nav()).getByRole('button', { name: 'Run the tests again' }))
    expect(within(nav()).getByRole('button', { name: 'Run the tests again' })).toHaveFocus()
    expect(preview()).toHaveTextContent('Run the tests again')
    await user.unhover(within(nav()).getByRole('button', { name: 'Run the tests again' }))
    expect(preview()).toBeNull()
  })

  it('when there are too many turns for the ruler, the ruler scrolls itself and keeps the ticks of visible turns in view', () => {
    // 60 turns compressed to the minimum spacing of 8px: the ticks start at 24px, and the ruler's visible height is 200px.
    ruler.scrollHeight = 24 + 60 * 8 + 24
    ruler.clientHeight = 200
    Object.defineProperty(HTMLElement.prototype, 'offsetTop', {
      configurable: true,
      get(this: HTMLElement) {
        return this.tagName === 'LI' ? 24 + Array.prototype.indexOf.call(this.parentElement!.children, this) * 8 : 0
      },
    })
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      get(this: HTMLElement) {
        return this.tagName === 'LI' ? 8 : 0
      },
    })
    const texts = Array.from({ length: 60 }, (_, i) => `Question ${i + 1}`)
    render(<List pages={pagesOf(texts)} />)
    const list = nav().querySelector('ol')!
    act(() => {
      FakeIntersectionObserver.trigger(turnBlock('Question 59'), true)
      FakeIntersectionObserver.trigger(turnBlock('Question 60'), true)
    })
    // The ticks for turns 59 and 60 span 24+58*8=488 to 24+60*8=504; leaving 16px below means scrolling to 520-200=320.
    expect(list.scrollTop).toBe(320)
    act(() => {
      FakeIntersectionObserver.trigger(turnBlock('Question 59'), false)
      FakeIntersectionObserver.trigger(turnBlock('Question 60'), false)
      FakeIntersectionObserver.trigger(turnBlock('Question 2'), true)
    })
    // The tick for turn 2 is at 32; leave 16px above it.
    expect(list.scrollTop).toBe(16)
  })
})

// Floating PrecisionRuler overlay: zero-width-tax vernier tick rail replacing the old 74px permanent stub.
describe('floating PrecisionRuler in chat', () => {
  it('mounts as a floating PrecisionRuler overlay with zero horizontal layout stub', () => {
    render(<List pages={pagesOf(['One', 'Two'])} />)
    expect(nav()).toHaveAttribute('data-precision-ruler')
    expect(nav()).toHaveClass('absolute', 'right-1.5', 'pointer-events-none', 'max-nav:hidden')
    expect(document.querySelector('[data-turn-stub]')).toBeNull()
  })

  it('marks every 5th turn as a major tick, shows #N badges on hover preview, and displays the total at the bottom', async () => {
    render(<List pages={pagesOf(texts(12))} turnsBefore={0} turnsTotal={12} />)
    expect(ticks()).toHaveLength(12)
    expect(majorTicks().map((tick) => tick.getAttribute('aria-label'))).toEqual(['Question 5', 'Question 10'])
    await userEvent.setup().hover(within(nav()).getByRole('button', { name: 'Question 5' }))
    const preview = nav().querySelector('[data-ruler-preview]')!
    expect(preview.querySelector('[data-ruler-preview-badge]')).toHaveTextContent('#5')
    expect(preview).toHaveTextContent('Question 5')
    expect(total()).toHaveTextContent('12')
    expect(total()).toHaveTextContent('12 turns in total')
  })

  it('while the turn count is unknown (history not fetched yet): only minor ticks, no major ticks, badges or total', async () => {
    render(<List pages={pagesOf(texts(12))} hasOlder={false} />)
    expect(ticks()).toHaveLength(12)
    expect(majorTicks()).toHaveLength(0)
    expect(total()).toBeNull()
    await userEvent.setup().hover(within(nav()).getByRole('button', { name: 'Question 5' }))
    expect(nav().querySelector('[data-ruler-preview-badge]')).toBeNull()
  })

  it('numbers on when there are turns before (also when earlier messages are not loaded)', async () => {
    render(<List pages={pagesOf(texts(4))} hasOlder turnsBefore={22} turnsTotal={26} />)
    expect(majorTicks().map((tick) => tick.getAttribute('aria-label'))).toEqual(['Question 3'])
    await userEvent.setup().hover(within(nav()).getByRole('button', { name: 'Question 3' }))
    expect(nav().querySelector('[data-ruler-preview-badge]')).toHaveTextContent('#25')
    expect(total()).toHaveTextContent('26')
  })

  it('the bottom shows the total it was given, not the number of loaded turns (when locating there are unloaded turns after)', () => {
    const pages = pagesOf(texts(6))
    render(<List pages={pages} hasOlder={false} targetRunId={pages.pages[0].messages[0].run.id} hasNewer onLoadNewer={() => {}} turnsBefore={0} turnsTotal={150} />)
    expect(majorTicks().map((tick) => tick.getAttribute('aria-label'))).toEqual(['Question 5'])
    expect(total()).toHaveTextContent('150')
    expect(total()).toHaveTextContent('150 turns in total')
  })

  it('draws a contiguous range bar across multiple visible ticks and clears it when none are visible', () => {
    render(<List pages={pagesOf(texts(8))} turnsBefore={0} turnsTotal={8} />)
    expect(rangeMarks()).toHaveLength(0)
    act(() => {
      for (const text of ['Question 6', 'Question 7', 'Question 8']) FakeIntersectionObserver.trigger(turnBlock(text), true)
    })
    expect(rangeMarks()).toHaveLength(1)
    expect(rangeMarks()[0].closest('li')).toContainElement(within(nav()).getByRole('button', { name: 'Question 6' }))
    expect(rangeMarks()[0]).toHaveAttribute('data-count', '3')
    expect(rangeMarks()[0]).toHaveAttribute('aria-hidden', 'true')
    act(() => {
      for (const text of ['Question 6', 'Question 7', 'Question 8']) FakeIntersectionObserver.trigger(turnBlock(text), false)
    })
    expect(rangeMarks()).toHaveLength(0)
  })

  it('scrolls itself internally when there are many turns, with hidden scrollbars', () => {
    render(<List pages={pagesOf(['One', 'Two'])} />)
    const list = nav().querySelector('ol')!
    expect(list).toHaveClass('overflow-y-auto', 'overflow-x-hidden', 'no-scrollbar')
    for (const item of list.querySelectorAll('li')) expect(item).toHaveClass('flex-[0_1_18px]', 'min-h-2.5')
  })

  it('renders nothing at all with fewer than 2 turns (zero empty stub column)', () => {
    const { rerender } = render(<List pages={pagesOf(['The only question'])} hasOlder={false} />)
    expect(document.querySelector('[data-precision-ruler]')).toBeNull()
    expect(document.querySelector('[data-turn-stub]')).toBeNull()
    rerender(<List pages={pagesOf(['One', 'Two'])} hasOlder={false} />)
    expect(document.querySelector('[data-precision-ruler]')).toBe(nav())
  })

  it('hides the native scrollbar on desktop only when a turn ruler is mounted', () => {
    const { rerender } = render(<List pages={pagesOf(['Only one'])} />)
    const log = screen.getByRole('log')
    expect(log).toHaveClass('overflow-y-auto')
    expect(log).not.toHaveClass('nav:no-scrollbar')
    rerender(<List pages={pagesOf(['One', 'Two'])} />)
    expect(log).toHaveClass('nav:no-scrollbar')
  })

  it('clicking the ruler bottom/latest control jumps to the true bottom and re-enables auto-follow', async () => {
    const pages = pagesOf(['One', 'Two', 'Three'])
    const { rerender } = render(<List pages={pages} turnsBefore={0} turnsTotal={3} />)
    const el = screen.getByRole('log')
    el.getBoundingClientRect = () => DOMRect.fromRect({ x: 0, y: 100, width: 600, height: 500 })
    turnBlock('One').getBoundingClientRect = () => DOMRect.fromRect({ x: 0, y: 100 - 1420, width: 600, height: 300 })

    const user = userEvent.setup()
    await user.click(within(nav()).getByRole('button', { name: 'One' }))
    expect(el.scrollTop).toBe(56)

    await user.click(within(nav()).getByRole('button', { name: 'Jump to bottom' }))
    expect(el.scrollTop).toBe(1500)
    // Auto-follow is re-enabled: growing content now pulls to the new bottom.
    log.scrollHeight = 2400
    const messages = pages.pages[0].messages
    rerender(<List pages={{ ...pages, pages: [{ ...pages.pages[0], messages: [...messages.slice(0, -1), { ...messages.at(-1)!, text: 'Answer to Three, grown' }] }] }} turnsBefore={0} turnsTotal={3} />)
    expect(el.scrollTop).toBe(1900)
  })
})

describe('inline turn numbers', () => {
  const turnNumbers = () => [...document.querySelectorAll('[data-turn-number]')].map((node) => node.textContent)

  it('each turn carries a subtle inline monospace turn number tag matching the ruler', () => {
    render(<List pages={pagesOf(texts(6))} turnsBefore={0} turnsTotal={6} />)
    expect(turnNumbers()).toEqual(['1', '2', '3', '4', '5', '6'])
    const number = turnBlock('Question 5').querySelector('[data-turn-number]')!
    expect(number).toHaveAttribute('aria-hidden', 'true')
    expect(number).toHaveClass('font-mono', 'text-[12px]', 'tabular-nums')
  })

  it('shows no numbers while the turn count is unknown', () => {
    render(<List pages={pagesOf(texts(3))} hasOlder={false} />)
    expect(turnNumbers()).toEqual(['', '', ''])
  })

  it('numbers on when there are turns before', () => {
    render(<List pages={pagesOf(texts(2))} hasOlder turnsBefore={24} turnsTotal={26} />)
    expect(turnNumbers()).toEqual(['25', '26'])
  })
})

describe('tick names', () => {
  const turn = (user: Partial<Schemas['Message']> | null, reply: string | null = null): Turn => ({
    key: 'k',
    user: user && makeMessage(user),
    run: makeRun(),
    reply: reply === null ? null : { text: reply, live: false },
    sessionEpoch: 1,
  })

  it('collapses whitespace in the user message; truncates when too long', () => {
    expect(turnPreview(turn({ text: '  First line\n\nSecond line\tend ' }))).toBe('First line Second line end')
    expect(turnPreview(turn({ text: '字'.repeat(100) }))).toBe(`${'字'.repeat(80)}…`) // check-english: zh (truncation counts characters of multibyte text, not bytes)
  })

  it('with only images gives the image count; platform reminders and sleep wakes say which kind they are', () => {
    const image: Schemas['MessageImage'] = { id: '00000000-0000-4000-8000-000000000001', mimeType: 'image/png', size: 1, width: 1, height: 1 }
    expect(turnPreview(turn({ text: '', images: [image, image] }))).toBe('2 images')
    expect(turnPreview(turn({ origin: 'inbox_reminder', text: '[platform] inbox' }))).toBe('Platform reminder: Received an Inbox message')
    const wake = 'Platform notice: your sleep is due; continue in the original conversation. sleepId=0b0c'
    expect(turnPreview(turn({ origin: 'sleep_wake', text: `${wake}; note: check  the build\nresult` }))).toBe('Woke up on schedule: check the build result')
    expect(turnPreview(turn({ origin: 'sleep_wake', text: wake }))).toBe('Woke up on schedule')
  })

  it('without a user message uses the start of the reply; when the reply has no text either, a generic phrase', () => {
    expect(turnPreview(turn(null, 'The build is fixed now'))).toBe('The build is fixed now')
    expect(turnPreview(turn(null, ''))).toBe('Turn without text')
    expect(turnPreview(turn({ text: '' }, null))).toBe('Turn without text')
  })
})

// Centered conversation stream without a 48px left number gutter.
describe('centered conversation stream', () => {
  it('centers the conversation stream in a max-w-[960px] column with full-column reply prose', () => {
    render(<List pages={pagesOf(texts(2))} turnsBefore={0} turnsTotal={2} />)
    const column = screen.getByRole('log').querySelector('[data-message-column]')!
    expect(column).toHaveClass('mx-auto', 'max-w-[960px]')
    const body = turnBlock('Question 1').querySelector('article')!.parentElement!
    expect(body).toHaveClass('min-w-0', 'w-full')
  })

  it('caps user bubbles at 82% inside the centered column', () => {
    render(<List pages={pagesOf(texts(2))} turnsBefore={0} turnsTotal={2} />)
    expect(screen.getByText('Question 1')).toHaveClass('max-w-[82%]')
  })

  it('aligns inserted flow items directly with the centered conversation column without a left gutter shift', () => {
    const inserts = [{ key: 'note', at: '2026-10-25T09:00:30Z', node: <p>A message inserted into the flow</p> }]
    render(<List pages={pagesOf(texts(2))} turnsBefore={0} turnsTotal={2} inserts={inserts} />)
    expect(screen.getByText('A message inserted into the flow').parentElement).not.toHaveClass('nav:pl-12')
  })
})
