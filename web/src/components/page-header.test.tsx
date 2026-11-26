import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it, vi } from 'vitest'
import { SidebarContext } from '@/app/sidebar-context'
import { LiveDot, PageFrame, PageHeader } from './PageHeader'

// "Printed sheet" redesign, step 3: the page title shared by project and global pages (not the Agent page header).
describe('Page title', () => {
  const renderHeader = (meta?: React.ReactNode) =>
    render(
      <MemoryRouter>
        <PageHeader crumbs={[{ label: 'Demo', to: '/?project=Demo' }]} title="Tasks">
          {meta}
        </PageHeader>
      </MemoryRouter>,
    )

  it('breadcrumb: the earlier levels are links, the last item is the current page in primary ink (riso blue in Plate skin)', () => {
    renderHeader()
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumb' })
    expect(within(crumbs).getByRole('link', { name: 'Demo' })).toHaveAttribute('href', '/?project=Demo')
    const current = within(crumbs).getByText('Tasks')
    expect(current).toHaveAttribute('aria-current', 'page')
    expect(current).toHaveClass('text-ink', '[[data-skin=plate]_&]:text-riso-blue')
  })

  it('the title uses compact 14px bar typography by default and preserves ZCOOL display styling with pink misregistration in Plate skin', () => {
    renderHeader()
    const title = screen.getByRole('heading', { level: 1, name: 'Tasks' })
    expect(title).toHaveClass('text-[14px]', 'font-semibold', 'tracking-[-0.01em]', 'text-ink', 'truncate')
    expect(title).toHaveClass(
      '[[data-skin=plate]_&]:font-display-cn',
      '[[data-skin=plate]_&]:text-riso-blue',
      '[[data-skin=plate]_&]:text-[clamp(38px,6vw,60px)]',
      '[[data-skin=plate]_&]:[text-shadow:4px_4px_0_var(--riso-pink)]',
      '[[data-skin=plate]_&]:nav:text-[40px]',
      '[[data-skin=plate]_&]:nav:[text-shadow:3px_3px_0_var(--riso-pink)]',
    )
  })

  it('single-row 48px bar in mono skin: meta and actions sit inline with title; right padding for OverprintDeco is scoped to Plate skin only', () => {
    render(
      <MemoryRouter>
        <PageHeader
          crumbs={[{ label: 'Demo', to: '/?project=Demo' }]}
          title="Tasks"
          actions={<button type="button">New task</button>}
          note="Running tasks update live."
        >
          <span>Tasks in this project</span>
        </PageHeader>
      </MemoryRouter>,
    )
    const header = screen.getByRole('banner')
    const bar = header.querySelector('[data-page-bar]')!
    expect(bar).toHaveClass('min-h-12', 'border-b', 'border-hairline', 'bg-surface-canvas', 'px-4', 'sm:px-6')
    const row = header.querySelector('[data-page-title-row]')!
    expect(row).toContainElement(screen.getByRole('heading', { level: 1 }))
    expect(row).toContainElement(screen.getByText('Tasks in this project'))
    expect(row).toContainElement(screen.getByRole('button', { name: 'New task' }))
    expect(row).toHaveClass(
      'flex',
      'flex-wrap',
      'items-center',
      '[[data-skin=plate]_&]:@[820px]:pr-[316px]',
      '[[data-skin=plate]_&]:nav:@[820px]:pr-[196px]',
    )
    expect(row).not.toHaveClass('@[820px]:pr-[316px]', 'nav:@[820px]:pr-[196px]')
    expect(header.querySelector('[data-page-actions]')).toHaveClass('ml-auto')
    expect(header.querySelector('[data-page-note]')).toHaveClass('border-b', 'border-hairline', 'bg-surface-subtle/50')
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' }).querySelector('ol')).toHaveClass('nav:leading-4')
    expect(header.querySelector('[data-overprint-deco]')).toHaveClass('nav:scale-[0.6]')
  })

  it('the meta container renders inline when provided; the overprint decoration at the top right is hidden from screen readers and not shown when narrow', () => {
    renderHeader(<span>Tasks in this project</span>)
    const header = screen.getByRole('banner')
    expect(header).toHaveClass('@container', 'relative')
    expect(within(header).getByText('Tasks in this project').closest('[data-page-meta]')).not.toBeNull()
    const deco = header.querySelector('[data-overprint-deco]')
    expect(deco).toHaveAttribute('aria-hidden', 'true')
    expect(deco).toHaveClass('hidden', '@[820px]:block')
  })

  it('leaves no empty line when there is no meta', () => {
    renderHeader()
    expect(document.querySelector('[data-page-meta]')).toBeNull()
  })

  it('page frame: full-bleed p-0 in mono skin, preserving fluid side padding and extra bottom room in Plate skin', () => {
    render(<PageFrame data-testid="frame">Body</PageFrame>)
    expect(screen.getByTestId('frame')).toHaveClass(
      'flex',
      'h-full',
      'min-h-0',
      'flex-col',
      'p-0',
      '[[data-skin=plate]_&]:px-[clamp(14px,2.4vw,34px)]',
      '[[data-skin=plate]_&]:pb-[26px]',
    )
  })

  it('keeps SidebarExpandButton clickable inside the window-drag bar with macOS traffic-light offset when collapsed in desktop mode', () => {
    const prev = window.loopNowDesktop
    window.loopNowDesktop = { platform: 'darwin', notify: vi.fn(), setBadge: vi.fn(), onNavigate: () => () => {} }
    try {
      render(
        <MemoryRouter>
          <SidebarContext.Provider value={{ collapsed: true, expand: vi.fn() }}>
            <PageHeader crumbs={[{ label: 'Demo', to: '/?project=Demo' }]} title="Tasks" />
          </SidebarContext.Provider>
        </MemoryRouter>,
      )
      const bar = screen.getByRole('banner').querySelector('[data-page-bar]')!
      expect(bar).toHaveClass('window-drag', 'nav:pl-[84px]')
      const expand = within(bar as HTMLElement).getByRole('button', { name: 'Expand navigation' })
      expect(expand).toHaveClass('window-no-drag')
    } finally {
      window.loopNowDesktop = prev
    }
  })
})

describe('Live status', () => {
  it('when connected: a green dot with a note such as “Live”; when disconnected: a red dot with reconnecting', () => {
    const { rerender } = render(<LiveDot state="open" label="Live" />)
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent('Live')
    expect(status).toHaveClass('text-green-text')
    expect(status.querySelector('[aria-hidden="true"]')).toHaveClass('bg-green')
    rerender(<LiveDot state="reconnecting" label="Live" />)
    expect(screen.getByRole('status')).toHaveTextContent('reconnecting')
    expect(screen.getByRole('status')).toHaveClass('text-red-text')
  })

  it('while still connecting it only holds the space and does not flash red', () => {
    render(<LiveDot state="connecting" label="Live" />)
    expect(screen.getByRole('status')).toBeEmptyDOMElement()
  })
})
