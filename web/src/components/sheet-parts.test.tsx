import { createRef, useState } from 'react'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { indexTabClass } from '@/lib/index-tab'
import { IndexTabLink, IndexTabs, SubNav } from './IndexTabs'
import { HollowNumber, NumberedSection } from './NumberedSection'
import { PageBody, RulerMark, RulerTicks, Sheet, Stub } from './Sheet'
import { StickyNote } from './StickyNote'

// Shared parts of the "printed sheet" redesign: sheet, page body, index tab / sub-nav, stub and ruler, numbered section heading, yellow sticky note.
describe('Sheet', () => {
  it('uses clean borderless canvas in mono skin while preserving the paper sheet and shadow in Plate skin', () => {
    render(<Sheet data-testid="sheet">Body</Sheet>)
    const sheet = screen.getByTestId('sheet')
    expect(sheet).toHaveTextContent('Body')
    expect(sheet).toHaveAttribute('data-sheet')
    expect(sheet).toHaveClass('relative', 'flex', 'min-h-0', 'flex-1', 'flex-col', 'bg-surface-canvas', 'overflow-hidden', 'riso-grain')
    expect(sheet).not.toHaveClass('border-t')
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

describe('PageBody', () => {
  it('applies width presets, forwards ref, and supports innerClassName', () => {
    const ref = createRef<HTMLDivElement>()
    const { rerender } = render(
      <PageBody ref={ref} data-testid="body" width="narrow" innerClassName="flex flex-col gap-6">
        Content
      </PageBody>,
    )
    const outer = screen.getByTestId('body')
    expect(ref.current).toBe(outer)
    expect(outer).toHaveClass('min-h-0', 'flex-1', 'overflow-y-auto')
    expect(outer.firstElementChild).toHaveClass('mx-auto', 'w-full', 'max-w-[780px]', 'px-4', 'py-5', 'sm:px-6', 'flex', 'flex-col', 'gap-6')

    rerender(<PageBody data-testid="body">Default</PageBody>)
    expect(screen.getByTestId('body').firstElementChild).toHaveClass('max-w-[1080px]')

    rerender(<PageBody data-testid="body" width="wide">Wide</PageBody>)
    expect(screen.getByTestId('body').firstElementChild).toHaveClass('max-w-[1320px]')

    rerender(<PageBody data-testid="body" width="full">Full</PageBody>)
    expect(screen.getByTestId('body').firstElementChild).toHaveClass('flex', 'h-full', 'min-h-0', 'w-full', 'flex-col')
    expect(screen.getByTestId('body').firstElementChild).not.toHaveClass('px-4')
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

  it('the tabs scroll sideways when they do not fit, without widening the page', () => {
    tabs('/chat')
    expect(screen.getByRole('navigation', { name: 'dev-helper' })).toHaveClass('h-9', 'overflow-x-auto', 'no-scrollbar', 'px-4', 'sm:px-6')
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

  it('SubNav supports route links with counts', () => {
    render(
      <MemoryRouter initialEntries={['/plugins']}>
        <SubNav
          aria-label="Plugins"
          items={[
            { key: 'installed', label: 'Installed', to: '/plugins', end: true, count: 3 },
            { key: 'market', label: 'Market', to: '/plugins/market' },
          ]}
        />
      </MemoryRouter>,
    )
    const nav = screen.getByRole('navigation', { name: 'Plugins' })
    const installed = within(nav).getByRole('link', { name: /Installed/ })
    expect(installed).toHaveAttribute('aria-current', 'page')
    expect(installed).toHaveTextContent('Installed3')
    expect(within(nav).getByRole('link', { name: 'Market' })).not.toHaveAttribute('aria-current')
  })

  it('SubNav supports controlled tablist mode with ArrowLeft, ArrowRight, Home, End keyboard navigation', async () => {
    const user = userEvent.setup()
    function Harness() {
      const [active, setActive] = useState<'all' | 'running' | 'failed' | 'done'>('all')
      return (
        <SubNav
          aria-label="Task filters"
          value={active}
          onChange={setActive}
          items={[
            { key: 'all', label: 'All', count: 5 },
            { key: 'running', label: 'Running', count: 2 },
            { key: 'failed', label: 'Failed', disabled: true },
            { key: 'done', label: 'Done', count: 3 },
          ]}
        />
      )
    }
    render(<Harness />)
    const tablist = screen.getByRole('tablist', { name: 'Task filters' })
    const all = within(tablist).getByRole('tab', { name: /All/ })
    const running = within(tablist).getByRole('tab', { name: /Running/ })
    const done = within(tablist).getByRole('tab', { name: /Done/ })
    expect(all).toHaveAttribute('aria-selected', 'true')

    all.focus()
    await user.keyboard('{ArrowRight}')
    expect(running).toHaveAttribute('aria-selected', 'true')
    expect(running).toHaveFocus()

    // Skips disabled "Failed" tab
    await user.keyboard('{ArrowRight}')
    expect(done).toHaveAttribute('aria-selected', 'true')
    expect(done).toHaveFocus()

    await user.keyboard('{Home}')
    expect(all).toHaveAttribute('aria-selected', 'true')
    expect(all).toHaveFocus()

    await user.keyboard('{End}')
    expect(done).toHaveAttribute('aria-selected', 'true')
    expect(done).toHaveFocus()

    await user.keyboard('{ArrowLeft}')
    expect(running).toHaveAttribute('aria-selected', 'true')
    expect(running).toHaveFocus()
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

  it('a section is a region named by its title: number hidden in mono skin for clean edge alignment, compact 14.5px semibold heading', () => {
    render(
      <NumberedSection number={3} title="Local directories">
        <p>Section content</p>
      </NumberedSection>,
    )
    const region = screen.getByRole('region', { name: 'Local directories' })
    expect(within(region).getByRole('heading', { level: 2, name: 'Local directories' })).toHaveClass(
      'text-[14.5px]',
      'font-semibold',
      'riso-marker',
      '[[data-skin=plate]_&]:font-display-cn',
    )
    const num = within(region).getByText('03')
    expect(num).toHaveAttribute('aria-hidden', 'true')
    expect(num).toHaveClass('hidden', '[[data-skin=plate]_&]:block')
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
