import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { indexTabClass } from '@/lib/index-tab'
import { IndexTabLink, IndexTabs } from './IndexTabs'
import { HollowNumber, NumberedSection } from './NumberedSection'
import { RulerMark, RulerTicks, Sheet, Stub } from './Sheet'
import { StickyNote } from './StickyNote'

// Shared parts of the "printed sheet" redesign: sheet, index tab, stub and ruler, numbered section heading, yellow sticky note.
describe('Sheet', () => {
  it('puts the content on a sheet: light paper, navy border, large radius, solid riso-blue shadow, room on the right for the shadow', () => {
    render(<Sheet data-testid="sheet">Body</Sheet>)
    const sheet = screen.getByTestId('sheet')
    expect(sheet).toHaveTextContent('Body')
    expect(sheet).toHaveAttribute('data-sheet')
    expect(sheet).toHaveClass('bg-paper-raised', 'border-2', 'border-ink', 'rounded-panel', 'shadow-print-dialog', 'overflow-hidden', 'mr-1.5')
    // The sheet surface also carries the main area's very faint paper grain.
    expect(sheet).toHaveClass('riso-grain')
  })

  it('scrollbars in the sheet are thin translucent navy ones (the sheet itself and every scrolling area inside it), not the grey system scrollbars', () => {
    render(<Sheet data-testid="sheet"><div className="overflow-y-auto">Body</div></Sheet>)
    expect(screen.getByTestId('sheet')).toHaveClass('riso-scrollbar')
  })

  it('the caller can add class names and attributes', () => {
    render(<Sheet data-testid="sheet" className="flex-1" aria-busy="true" />)
    expect(screen.getByTestId('sheet')).toHaveClass('flex-1', 'bg-paper-raised')
    expect(screen.getByTestId('sheet')).toHaveAttribute('aria-busy', 'true')
  })
})

describe('Index tabs', () => {
  const tabs = (path: string) =>
    render(
      <MemoryRouter initialEntries={[path]}>
        <IndexTabs aria-label="dev-helper">
          <IndexTabLink to="/chat">Chat</IndexTabLink>
          <IndexTabLink to="/config">Config</IndexTabLink>
        </IndexTabs>
      </MemoryRouter>,
    )

  it('is a named group of navigation links with the current page marked', () => {
    tabs('/config')
    const nav = screen.getByRole('navigation', { name: 'dev-helper' })
    expect(within(nav).getAllByRole('link').map((link) => link.textContent)).toEqual(['Chat', 'Config'])
    expect(within(nav).getByRole('link', { name: 'Config' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: 'Chat' })).not.toHaveAttribute('aria-current')
  })

  it('the current page is yellow and 40px tall; the others are beige-grey and 34px tall; all have a border on the top, left and right only', () => {
    tabs('/chat')
    const current = screen.getByRole('link', { name: 'Chat' })
    const other = screen.getByRole('link', { name: 'Config' })
    expect(current).toHaveClass('h-10', 'bg-riso-yellow', 'text-ink', 'border-2', 'border-b-0', 'border-ink')
    expect(other).toHaveClass('h-[34px]', 'bg-paper-tab', 'text-ink-700', 'border-2', 'border-b-0', 'border-ink')
  })

  it('on narrow screens the tabs get narrower and scroll sideways when they do not fit, without widening the page', () => {
    tabs('/chat')
    expect(screen.getByRole('navigation', { name: 'dev-helper' })).toHaveClass('max-nav:overflow-x-auto', 'max-nav:no-scrollbar')
    expect(screen.getByRole('link', { name: 'Chat' })).toHaveClass('max-sm:px-2.5')
    expect(screen.getByRole('link', { name: 'Config' })).toHaveClass('max-sm:px-2.5')
  })

  it('button-style tabs (status filters, categories) use the same styles', () => {
    expect(indexTabClass(true)).toContain('bg-riso-yellow')
    expect(indexTabClass(false)).toContain('bg-paper-tab')
    expect(indexTabClass(true)).not.toContain('bg-paper-tab')
  })
})

describe('Stub and ruler', () => {
  it('the stub has a small heading separated from the body by a dashed line, and is hidden on narrow screens', () => {
    render(
      <Stub label="Overview" data-testid="stub">
        <span>4 files</span>
      </Stub>,
    )
    const stub = screen.getByTestId('stub')
    expect(stub).toHaveTextContent('Overview4 files')
    expect(stub).toHaveClass('border-l-2', 'border-dashed', 'max-nav:hidden')
    expect(screen.getByText('Overview')).not.toHaveAttribute('aria-hidden')
  })

  it('a stub used as a navigation region is named with aria-label, and its visible heading is not read out again', () => {
    render(
      <Stub as="nav" aria-label="Conversation turns" label="TURNS">
        <button type="button">Turn 1</button>
      </Stub>,
    )
    const nav = screen.getByRole('navigation', { name: 'Conversation turns' })
    expect(within(nav).getByText('TURNS')).toHaveAttribute('aria-hidden', 'true')
    expect(within(nav).getByRole('button', { name: 'Turn 1' })).toBeInTheDocument()
  })

  it('the ruler ticks and highlighter mark are decoration, hidden from screen readers', () => {
    render(
      <>
        <RulerTicks data-testid="ticks" />
        <RulerMark data-testid="mark" style={{ top: 12, height: 40 }} />
      </>,
    )
    expect(screen.getByTestId('ticks')).toHaveAttribute('aria-hidden', 'true')
    const mark = screen.getByTestId('mark')
    expect(mark).toHaveAttribute('aria-hidden', 'true')
    expect(mark).toHaveStyle({ top: '12px', height: '40px' })
    // A yellow highlighter mark with a small pink arrow on the left.
    expect(mark.querySelector('[data-ruler-band]')).toHaveClass('bg-riso-yellow')
    expect(mark.querySelector('[data-ruler-arrow]')).toHaveClass('border-l-riso-pink')
  })
})

describe('Numbered section', () => {
  it('the hollow number is decoration and hidden on narrow screens', () => {
    render(<HollowNumber data-testid="number">25</HollowNumber>)
    const number = screen.getByTestId('number')
    expect(number).toHaveTextContent('25')
    expect(number).toHaveAttribute('aria-hidden', 'true')
    expect(number).toHaveClass('max-nav:hidden', 'text-transparent')
  })

  it('a section is a region named by its title: a two-digit number on the left, the title is a level-2 heading with a yellow highlighter stroke', () => {
    render(
      <NumberedSection number={3} title="Local directories">
        <p>Section content</p>
      </NumberedSection>,
    )
    const region = screen.getByRole('region', { name: 'Local directories' })
    expect(within(region).getByRole('heading', { level: 2, name: 'Local directories' })).toHaveClass('font-display-cn', 'riso-marker')
    expect(within(region).getByText('03')).toHaveAttribute('aria-hidden', 'true')
    expect(region).toHaveTextContent('Section content')
  })

  it('the title can be followed by a note and right-aligned actions; from the second section on there is a dashed line above', () => {
    const { rerender } = render(
      <NumberedSection number={1} title="Usage" meta={<span>Unit: tokens</span>} actions={<button type="button">Today</button>}>
        <p>Section content</p>
      </NumberedSection>,
    )
    const region = screen.getByRole('region', { name: 'Usage' })
    const heading = within(region).getByRole('heading', { level: 2, name: 'Usage' })
    expect(heading.parentElement).toContainElement(within(region).getByText('Unit: tokens'))
    expect(within(region).getByRole('button', { name: 'Today' }).parentElement).toHaveClass('sm:ml-auto')
    expect(region.querySelector('[data-section-number]')).toHaveTextContent('01')
    expect(region).not.toHaveClass('border-t-2')
    rerender(
      <NumberedSection number={2} title="Usage">
        <p>Section content</p>
      </NumberedSection>,
    )
    expect(screen.getByRole('region', { name: 'Usage' })).toHaveClass('border-t-2', 'border-dashed')
  })
})

describe('Yellow sticky note', () => {
  it('is a note (role=note) with a decorative info icon', () => {
    render(<StickyNote>Changes take effect from the next message.</StickyNote>)
    const note = screen.getByRole('note')
    expect(note).toHaveTextContent('Changes take effect from the next message.')
    expect(note).toHaveClass('riso-tilt')
    expect(note.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })
})
