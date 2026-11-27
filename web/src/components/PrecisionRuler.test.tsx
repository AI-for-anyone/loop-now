import { useRef } from 'react'
import { MemoryRouter } from 'react-router'
import { act, fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { FakeIntersectionObserver } from '@/test/intersection-observer'
import {
  PageRuler,
  PrecisionRuler,
  ScrollRuler,
  type PrecisionRulerItem,
  type SectionRulerAnchor,
} from './PrecisionRuler'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

const sampleItems: PrecisionRulerItem[] = [
  { id: 'one', label: 'First item', badge: '01', major: true },
  { id: 'two', label: 'Second item', sublabel: '2m ago' },
  { id: 'three', label: 'Third item', tone: 'success' },
  { id: 'four', label: 'Fourth item', tone: 'warning' },
  { id: 'five', label: 'Danger zone', badge: '05', major: true, tone: 'danger' },
]

describe('PrecisionRuler', () => {
  it('returns null when fewer items than minItems are provided', () => {
    const onSelect = vi.fn()
    const { rerender } = render(<PrecisionRuler items={[]} onSelect={onSelect} label="Sections" />)
    expect(screen.queryByRole('navigation')).toBeNull()

    rerender(<PrecisionRuler items={[sampleItems[0]]} onSelect={onSelect} label="Sections" />)
    expect(screen.queryByRole('navigation')).toBeNull()

    rerender(<PrecisionRuler items={[sampleItems[0]]} onSelect={onSelect} label="Sections" minItems={1} />)
    expect(screen.getByRole('navigation', { name: 'Sections' })).toBeInTheDocument()
  })

  it('supports floating (default) and inline placements with data-precision-ruler and max-nav:hidden', () => {
    const { rerender } = render(<PrecisionRuler items={sampleItems} onSelect={vi.fn()} label="Turns" />)
    const nav = screen.getByRole('navigation', { name: 'Turns' })
    expect(nav).toHaveAttribute('data-precision-ruler')
    expect(nav).toHaveClass('absolute', 'pointer-events-none', 'max-nav:hidden')

    rerender(<PrecisionRuler items={sampleItems} onSelect={vi.fn()} label="Turns" placement="inline" className="custom-rail" />)
    expect(nav).toHaveClass('relative', 'shrink-0', 'max-nav:hidden', 'custom-rail')
    expect(nav).not.toHaveClass('pointer-events-none')
  })

  it('normalizes single string, array, and Set activeIds and renders a range bar for contiguous multi-active ticks', () => {
    const { rerender } = render(<PrecisionRuler items={sampleItems} activeIds="two" onSelect={vi.fn()} label="Turns" />)
    const nav = screen.getByRole('navigation', { name: 'Turns' })
    expect(within(nav).getByRole('button', { name: 'Second item' })).toHaveAttribute('aria-current', 'true')
    expect(nav.querySelector('[data-ruler-range]')).toBeNull()

    rerender(<PrecisionRuler items={sampleItems} activeIds={['two', 'three', 'four']} onSelect={vi.fn()} label="Turns" />)
    const activeButtons = within(nav)
      .getAllByRole('button')
      .filter((btn) => btn.getAttribute('aria-current') === 'true')
    expect(activeButtons.map((btn) => btn.getAttribute('aria-label'))).toEqual(['Second item', 'Third item', 'Fourth item'])
    const rangeBar = nav.querySelector('[data-ruler-range]')
    expect(rangeBar).not.toBeNull()
    expect(rangeBar).toHaveAttribute('data-count', '3')
    expect(rangeBar!.closest('li')).toContainElement(within(nav).getByRole('button', { name: 'Second item' }))

    rerender(<PrecisionRuler items={sampleItems} activeIds={new Set(['one', 'two'])} onSelect={vi.fn()} label="Turns" />)
    expect(nav.querySelector('[data-ruler-range]')).toHaveAttribute('data-count', '2')
  })

  it('applies major, active, and semantic tone styles to ticks', () => {
    const items: PrecisionRulerItem[] = [
      { id: 'reg', label: 'Regular' },
      { id: 'maj', label: 'Major', major: true },
      { id: 'act', label: 'Active' },
      { id: 'acc', label: 'Accent', tone: 'accent' },
      { id: 'ok', label: 'Success', tone: 'success' },
      { id: 'warn', label: 'Warning', tone: 'warning' },
      { id: 'err', label: 'Danger', tone: 'danger' },
    ]
    render(<PrecisionRuler items={items} activeIds="act" onSelect={vi.fn()} label="Tones" />)
    const tickOf = (name: string) => screen.getByRole('button', { name }).querySelector('[data-ruler-tick]')!

    expect(tickOf('Regular')).toHaveClass('w-2', 'bg-ink/25')
    expect(tickOf('Major')).toHaveClass('w-3.5', 'bg-ink/45')
    expect(tickOf('Active')).toHaveClass('w-5', 'bg-riso-blue')
    expect(tickOf('Accent')).toHaveClass('bg-riso-blue')
    expect(tickOf('Success')).toHaveClass('bg-green')
    expect(tickOf('Warning')).toHaveClass('bg-pink-text')
    expect(tickOf('Danger')).toHaveClass('bg-red-text')
  })

  it('invokes onSelect on click and displays a clamped hover/focus preview pill without sticking after pointer clicks', async () => {
    const onSelect = vi.fn()
    render(<PrecisionRuler items={sampleItems} onSelect={onSelect} label="Sections" />)
    const user = userEvent.setup()
    const nav = screen.getByRole('navigation', { name: 'Sections' })
    const preview = () => nav.querySelector<HTMLElement>('[data-ruler-preview]')

    nav.getBoundingClientRect = () => DOMRect.fromRect({ x: 500, y: 100, width: 28, height: 200 })
    const firstLi = nav.querySelectorAll('li')[0]
    firstLi.getBoundingClientRect = () => DOMRect.fromRect({ x: 500, y: 102, width: 28, height: 10 })

    const firstBtn = within(nav).getByRole('button', { name: 'First item' })
    await user.hover(firstBtn)
    expect(preview()).toBeInTheDocument()
    expect(preview()).toHaveAttribute('aria-hidden', 'true')
    expect(preview()).toHaveTextContent('01')
    expect(preview()).toHaveTextContent('First item')
    // Top is clamped to HINT_HALF (16px) so it does not clip above the nav container.
    expect(preview()!.style.top).toBe('16px')

    await user.unhover(firstBtn)
    expect(preview()).toBeNull()

    // Hovering an item with sublabel shows sublabel too.
    const secondBtn = within(nav).getByRole('button', { name: 'Second item' })
    await user.hover(secondBtn)
    expect(preview()).toHaveTextContent('Second item')
    expect(preview()).toHaveTextContent('2m ago')
    await user.unhover(secondBtn)
    expect(preview()).toBeNull()

    // Keyboard focus opens the preview and tabbing moves it along.
    act(() => firstBtn.focus())
    expect(preview()).toHaveTextContent('First item')
    await user.tab()
    expect(secondBtn).toHaveFocus()
    expect(preview()).toHaveTextContent('Second item')

    // Clicking an already-focused button transitions to pointer interaction and clears preview on unhover.
    await user.click(secondBtn)
    expect(onSelect).toHaveBeenCalledWith('two')
    await user.unhover(secondBtn)
    expect(preview()).toBeNull()

    // Clicking an unfocused button focuses it without leaving the preview stuck after pointer leaves.
    const thirdBtn = within(nav).getByRole('button', { name: 'Third item' })
    await user.click(thirdBtn)
    expect(onSelect).toHaveBeenCalledWith('three')
    expect(thirdBtn).toHaveFocus()
    await user.unhover(thirdBtn)
    expect(preview()).toBeNull()
  })

  it('auto-scrolls the tick list to keep active ticks in view when overflowing', () => {
    const manyItems: PrecisionRulerItem[] = Array.from({ length: 50 }, (_, i) => ({
      id: `item-${i + 1}`,
      label: `Item ${i + 1}`,
    }))
    let listScrollTop = 0
    const { rerender } = render(<PrecisionRuler items={manyItems} activeIds={null} onSelect={vi.fn()} label="Many" />)
    const nav = screen.getByRole('navigation', { name: 'Many' })
    const list = nav.querySelector('ol')!

    Object.defineProperty(list, 'scrollHeight', { configurable: true, value: 900 })
    Object.defineProperty(list, 'clientHeight', { configurable: true, value: 200 })
    Object.defineProperty(list, 'scrollTop', {
      configurable: true,
      get: () => listScrollTop,
      set: (value: number) => {
        listScrollTop = value
      },
    })
    Array.from(list.children).forEach((child, index) => {
      Object.defineProperty(child, 'offsetTop', { configurable: true, value: 8 + index * 16 })
      Object.defineProperty(child, 'offsetHeight', { configurable: true, value: 16 })
    })

    rerender(<PrecisionRuler items={manyItems} activeIds={['item-49', 'item-50']} onSelect={vi.fn()} label="Many" />)
    // Item 50 bottom is 8 + 50*16 = 808; + 16px margin = 824; 824 - 200 = 624.
    expect(listScrollTop).toBe(624)

    rerender(<PrecisionRuler items={manyItems} activeIds="item-2" onSelect={vi.fn()} label="Many" />)
    // Item 2 top is 24; - 16px margin = 8.
    expect(listScrollTop).toBe(8)
  })

  it('renders optional bottom jump button and total badge', async () => {
    const onJumpBottom = vi.fn()
    const { rerender } = render(
      <PrecisionRuler
        items={sampleItems}
        onSelect={vi.fn()}
        label="Turns"
        onJumpBottom={onJumpBottom}
        jumpBottomLabel="Jump to latest"
        total={42}
        totalLabel="42 turns in total"
      />,
    )
    const user = userEvent.setup()
    const nav = screen.getByRole('navigation', { name: 'Turns' })

    const latestBtn = within(nav).getByRole('button', { name: 'Jump to latest' })
    await user.click(latestBtn)
    expect(onJumpBottom).toHaveBeenCalledTimes(1)

    const totalEl = nav.querySelector('[data-ruler-total]')!
    expect(totalEl.tagName).toBe('BUTTON')
    expect(totalEl).toHaveTextContent('42')
    expect(totalEl).toHaveTextContent('42 turns in total')
    await user.click(totalEl)
    expect(onJumpBottom).toHaveBeenCalledTimes(2)

    rerender(<PrecisionRuler items={sampleItems} onSelect={vi.fn()} label="Turns" total={42} />)
    const staticTotal = nav.querySelector('[data-ruler-total]')!
    expect(staticTotal.tagName).toBe('DIV')
    expect(staticTotal).toHaveTextContent('42')
  })
})

describe('PageRuler and useSectionRuler', () => {
  const sections: SectionRulerAnchor[] = [
    { id: 'sec-general', titleId: 'title-general', title: 'General' },
    { id: 'sec-runtime', titleId: 'title-runtime', title: 'Runtime' },
    { id: 'sec-danger', titleId: 'title-danger', title: 'Danger zone', danger: true },
  ]

  function SectionPage({ initialEntry = '/' }: { initialEntry?: string }) {
    const rootRef = useRef<HTMLDivElement>(null)
    return (
      <MemoryRouter initialEntries={[initialEntry]}>
        <div ref={rootRef} className="relative">
          {sections.map((section) => (
            <section key={section.id} id={section.id}>
              <h2 id={section.titleId} tabIndex={-1}>
                {section.title}
              </h2>
            </section>
          ))}
          <PageRuler sections={sections} label="Page sections" rootRef={rootRef} />
        </div>
      </MemoryRouter>
    )
  }

  it('tracks visible sections via IntersectionObserver and styles danger entries', async () => {
    render(<SectionPage />)
    const nav = screen.getByRole('navigation', { name: 'Page sections' })
    const user = userEvent.setup()

    act(() => {
      FakeIntersectionObserver.trigger(document.getElementById('sec-runtime')!, true)
      FakeIntersectionObserver.trigger(document.getElementById('sec-danger')!, true)
    })
    // Topmost visible section wins.
    expect(within(nav).getByRole('button', { name: 'Runtime' })).toHaveAttribute('aria-current', 'true')
    expect(within(nav).getByRole('button', { name: 'Danger zone' })).not.toHaveAttribute('aria-current')

    const dangerTick = within(nav).getByRole('button', { name: 'Danger zone' }).querySelector('[data-ruler-tick]')!
    expect(dangerTick).toHaveClass('bg-red-text')

    // Hover preview displays padded 2-digit section index badge.
    await user.hover(within(nav).getByRole('button', { name: 'Danger zone' }))
    expect(nav.querySelector('[data-ruler-preview]')).toHaveTextContent('03')
    expect(nav.querySelector('[data-ruler-preview]')).toHaveTextContent('Danger zone')
  })

  it('scrolls and focuses section title when clicking a tick or matching a URL hash', async () => {
    const scrollIntoView = vi.fn()
    Element.prototype.scrollIntoView = scrollIntoView

    render(<SectionPage initialEntry="/settings#sec-runtime" />)
    expect(scrollIntoView).toHaveBeenCalledWith({ block: 'start' })
    expect(document.getElementById('title-runtime')).toHaveFocus()

    const user = userEvent.setup()
    const nav = screen.getByRole('navigation', { name: 'Page sections' })
    await user.click(within(nav).getByRole('button', { name: 'Danger zone' }))
    expect(within(nav).getByRole('button', { name: 'Danger zone' })).toHaveAttribute('aria-current', 'true')
    expect(document.getElementById('title-danger')).toHaveFocus()
  })
})

describe('ScrollRuler', () => {
  function ScrollContainer({
    scrollHeight,
    clientHeight,
    initialScrollTop = 0,
    tickCount,
  }: {
    scrollHeight: number
    clientHeight: number
    initialScrollTop?: number
    tickCount?: number
  }) {
    const containerRef = useRef<HTMLDivElement>(null)
    return (
      <div className="relative">
        <div
          ref={(node) => {
            containerRef.current = node
            if (!node) return
            let currentTop = initialScrollTop
            Object.defineProperty(node, 'scrollHeight', { configurable: true, get: () => scrollHeight })
            Object.defineProperty(node, 'clientHeight', { configurable: true, get: () => clientHeight })
            Object.defineProperty(node, 'scrollTop', {
              configurable: true,
              get: () => currentTop,
              set: (value: number) => {
                currentTop = Math.max(0, Math.min(value, Math.max(0, scrollHeight - clientHeight)))
              },
            })
          }}
          data-testid="scroller"
        />
        <ScrollRuler targetRef={containerRef} label="Page scroll" tickCount={tickCount} />
      </div>
    )
  }

  it('renders null when the container does not overflow beyond 40px', () => {
    render(<ScrollContainer scrollHeight={540} clientHeight={500} />)
    expect(document.querySelector('[data-scroll-ruler]')).toBeNull()
  })

  it('renders calibrated ticks, highlights the viewport thumb window, and reacts to scroll events', () => {
    render(<ScrollContainer scrollHeight={2000} clientHeight={500} initialScrollTop={0} />)
    const ruler = document.querySelector<HTMLElement>('[data-scroll-ruler]')
    expect(ruler).not.toBeNull()
    expect(ruler).toHaveClass('max-nav:hidden')

    const ticks = Array.from(ruler!.querySelectorAll<HTMLElement>('[data-scroll-tick]'))
    expect(ticks).toHaveLength(17)
    expect(ticks.filter((tick) => tick.dataset.major === 'true')).toHaveLength(5)
    expect(ticks[0]).toHaveAttribute('data-active', 'true')
    expect(ticks[16]).not.toHaveAttribute('data-active')

    const scroller = screen.getByTestId('scroller')
    scroller.scrollTop = 1500
    fireEvent.scroll(scroller)

    expect(ticks[0]).not.toHaveAttribute('data-active')
    expect(ticks[16]).toHaveAttribute('data-active', 'true')
    expect(screen.getByRole('slider', { name: 'Page scroll' })).toHaveAttribute('aria-valuenow', '100')
  })

  it('jumps scrollTop on pointer click/drag and reveals a percentage pill on hover', () => {
    render(<ScrollContainer scrollHeight={2000} clientHeight={500} initialScrollTop={630} />)
    const scroller = screen.getByTestId('scroller')
    const rail = screen.getByRole('slider', { name: 'Page scroll' })
    const ruler = document.querySelector<HTMLElement>('[data-scroll-ruler]')!

    ruler.getBoundingClientRect = () => DOMRect.fromRect({ x: 500, y: 100, width: 20, height: 200 })
    rail.getBoundingClientRect = () => DOMRect.fromRect({ x: 500, y: 100, width: 20, height: 200 })

    // Hovering displays the current scroll percentage (630 / 1500 = 42%).
    fireEvent.pointerEnter(rail, { clientY: 184 })
    expect(ruler.querySelector('[data-scroll-ruler-preview]')).toHaveTextContent('42%')

    // Clicking at midpoint (y = 200 -> ratio 0.5) jumps to scrollTop = 750 (50%).
    fireEvent.pointerDown(rail, { clientY: 200, pointerId: 1 })
    expect(scroller.scrollTop).toBe(750)
    expect(ruler.querySelector('[data-scroll-ruler-preview]')).toHaveTextContent('50%')

    // Dragging down to 80% (y = 260) updates scrollTop to 1200 (80%).
    fireEvent.pointerMove(rail, { clientY: 260, pointerId: 1 })
    expect(scroller.scrollTop).toBe(1200)
    expect(ruler.querySelector('[data-scroll-ruler-preview]')).toHaveTextContent('80%')

    fireEvent.pointerUp(rail, { clientY: 260, pointerId: 1 })
    fireEvent.pointerLeave(rail)
    expect(ruler.querySelector('[data-scroll-ruler-preview]')).toBeNull()
  })

  it('works cleanly when ResizeObserver is unavailable', () => {
    vi.stubGlobal('ResizeObserver', undefined)
    render(<ScrollContainer scrollHeight={1200} clientHeight={400} />)
    expect(screen.getByRole('slider', { name: 'Page scroll' })).toBeInTheDocument()
  })
})
