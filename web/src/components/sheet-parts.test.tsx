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
  it('uses clean canvas and hairline top border in mono skin while preserving the paper sheet and shadow in Plate skin', () => {
    render(<Sheet data-testid="sheet">Body</Sheet>)
    const sheet = screen.getByTestId('sheet')
    expect(sheet).toHaveTextContent('Body')
    expect(sheet).toHaveAttribute('data-sheet')
    expect(sheet).toHaveClass('bg-surface-canvas', 'border-t', 'border-hairline', 'overflow-hidden', 'riso-grain')
    expect(sheet).toHaveClass(
      '[[data-skin=plate]_&]:bg-paper-raised',
      '[[data-skin=plate]_&]:border-2',
      '[[data-skin=plate]_&]:border-ink',
      '[[data-skin=plate]_&]:rounded-panel',
      '[[data-skin=plate]_&]:shadow-print-dialog',
      '[[data-skin=plate]_&]:mr-1.5',
    )
  })

  it('scrollbars in the sheet use riso-scrollbar', () => {
    render(<Sheet data-testid="sheet"><div className="overflow-y-auto">Body</div></Sheet>)
    expect(screen.getByTestId('sheet')).toHaveClass('riso-scrollbar')
  })

  it('the caller can add class names and attributes', () => {
    render(<Sheet data-testid="sheet" className="flex-1" aria-busy="true" />)
    expect(screen.getByTestId('sheet')).toHaveClass('flex-1', 'bg-surface-canvas')
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

  it('uses hairline bottom-border underline tabs in mono skin and preserves physical yellow/beige folder tabs in Plate skin', () => {
    tabs('/chat')
    const current = screen.getByRole('link', { name: 'Chat' })
    const other = screen.getByRole('link', { name: 'Config' })
    expect(current).toHaveClass('border-b-2', 'border-ink', 'font-semibold', 'text-ink')
    expect(current).toHaveClass('[[data-skin=plate]_&]:h-10', '[[data-skin=plate]_&]:bg-riso-yellow', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:border-b-0')
    expect(other).toHaveClass('border-b-2', 'border-transparent', 'text-ink-500')
    expect(other).toHaveClass('[[data-skin=plate]_&]:h-[34px]', '[[data-skin=plate]_&]:bg-paper-tab', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:border-b-0')
  })

  it('on narrow screens the tabs scroll sideways when they do not fit, without widening the page', () => {
    tabs('/chat')
    expect(screen.getByRole('navigation', { name: 'dev-helper' })).toHaveClass('max-nav:overflow-x-auto', 'max-nav:no-scrollbar')
    expect(screen.getByRole('link', { name: 'Chat' })).toHaveClass('[[data-skin=plate]_&]:max-sm:px-2.5')
    expect(screen.getByRole('link', { name: 'Config' })).toHaveClass('[[data-skin=plate]_&]:max-sm:px-2.5')
  })

  it('button-style tabs (status filters, categories) use the same styles', () => {
    expect(indexTabClass(true)).toContain('border-ink')
    expect(indexTabClass(true)).toContain('[[data-skin=plate]_&]:bg-riso-yellow')
    expect(indexTabClass(false)).toContain('border-transparent')
    expect(indexTabClass(false)).toContain('[[data-skin=plate]_&]:bg-paper-tab')
    expect(indexTabClass(true)).not.toContain('bg-paper-tab')
  })
})

describe('Stub and ruler', () => {
  it('the stub has a hairline separator in mono skin (dashed in Plate skin) and is hidden on narrow screens', () => {
    render(
      <Stub label="Overview" data-testid="stub">
        <span>4 files</span>
      </Stub>,
    )
    const stub = screen.getByTestId('stub')
    expect(stub).toHaveTextContent('Overview4 files')
    expect(stub).toHaveClass('border-l', 'border-hairline', 'max-nav:hidden', '[[data-skin=plate]_&]:border-l-2', '[[data-skin=plate]_&]:border-dashed')
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

  it('the ruler ticks and mark are decoration; mix-blend-multiply is scoped to Plate skin only', () => {
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
    const band = mark.querySelector('[data-ruler-band]')!
    expect(band).toHaveClass('bg-surface-active', '[[data-skin=plate]_&]:bg-riso-yellow', '[[data-skin=plate]_&]:mix-blend-multiply')
    expect(band).not.toHaveClass('mix-blend-multiply')
    expect(mark.querySelector('[data-ruler-arrow]')).toHaveClass('bg-ink', '[[data-skin=plate]_&]:border-l-riso-pink')
  })
})

describe('Numbered section', () => {
  it('the section number is decorative tabular mono in mono skin and stroked outline in Plate skin', () => {
    render(<HollowNumber data-testid="number">25</HollowNumber>)
    const number = screen.getByTestId('number')
    expect(number).toHaveTextContent('25')
    expect(number).toHaveAttribute('aria-hidden', 'true')
    expect(number).toHaveClass('max-nav:hidden', 'tabular-nums', 'text-ink-500', '[[data-skin=plate]_&]:text-transparent')
  })

  it('a section is a region named by its title: a two-digit number on the left and a clean semibold heading', () => {
    render(
      <NumberedSection number={3} title="Local directories">
        <p>Section content</p>
      </NumberedSection>,
    )
    const region = screen.getByRole('region', { name: 'Local directories' })
    expect(within(region).getByRole('heading', { level: 2, name: 'Local directories' })).toHaveClass(
      'text-[16px]',
      'font-semibold',
      'riso-marker',
      '[[data-skin=plate]_&]:font-display-cn',
    )
    expect(within(region).getByText('03')).toHaveAttribute('aria-hidden', 'true')
    expect(region).toHaveTextContent('Section content')
  })

  it('the title can be followed by a note and right-aligned actions; from the second section on there is a hairline divider above', () => {
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
    expect(region).not.toHaveClass('border-t')
    rerender(
      <NumberedSection number={2} title="Usage">
        <p>Section content</p>
      </NumberedSection>,
    )
    expect(screen.getByRole('region', { name: 'Usage' })).toHaveClass(
      'border-t',
      'border-hairline',
      '[[data-skin=plate]_&]:border-t-2',
      '[[data-skin=plate]_&]:border-dashed',
    )
  })
})

describe('Sticky note', () => {
  it('is a note (role=note) with clean hairline callout styling in mono skin and riso-tilt preserved for Plate skin', () => {
    render(<StickyNote>Changes take effect from the next message.</StickyNote>)
    const note = screen.getByRole('note')
    expect(note).toHaveTextContent('Changes take effect from the next message.')
    expect(note).toHaveClass('border', 'border-hairline', 'bg-surface-subtle', 'rounded-card', 'riso-tilt')
    expect(note.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })
})
