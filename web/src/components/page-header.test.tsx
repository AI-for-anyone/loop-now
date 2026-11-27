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

  it('breadcrumb: the earlier levels are links, the last item is the current page in primary ink', () => {
    renderHeader()
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumb' })
    expect(within(crumbs).getByRole('link', { name: 'Demo' })).toHaveAttribute('href', '/?project=Demo')
    const current = within(crumbs).getByText('Tasks')
    expect(current).toHaveAttribute('aria-current', 'page')
    expect(current).toHaveClass('text-ink')
  })

  it('the title uses compact 14px bar typography', () => {
    renderHeader()
    const title = screen.getByRole('heading', { level: 1, name: 'Tasks' })
    expect(title).toHaveClass('text-[14px]', 'font-semibold', 'tracking-[-0.01em]', 'text-ink', 'truncate')
  })

  it('single-row 48px bar: meta and actions sit inline with title and note renders in a slim band below', () => {
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
    expect(row).toHaveClass('flex', 'flex-wrap', 'items-center')
    expect(header.querySelector('[data-page-actions]')).toHaveClass('ml-auto')
    expect(header.querySelector('[data-page-note]')).toHaveClass('border-b', 'border-hairline', 'bg-surface-subtle/50')
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' }).querySelector('ol')).toHaveClass('nav:leading-4')
  })

  it('the meta container renders inline when provided', () => {
    renderHeader(<span>Tasks in this project</span>)
    const header = screen.getByRole('banner')
    expect(header).toHaveClass('@container', 'relative')
    expect(within(header).getByText('Tasks in this project').closest('[data-page-meta]')).not.toBeNull()
  })

  it('leaves no empty line when there is no meta', () => {
    renderHeader()
    expect(document.querySelector('[data-page-meta]')).toBeNull()
  })

  it('page frame: fills the content area directly without outer margins; the header bar carries its own horizontal gutters', () => {
    render(
      <MemoryRouter>
        <PageFrame data-testid="frame">
          <PageHeader crumbs={[{ label: 'Demo', to: '/?project=Demo' }]} title="Tasks" />
        </PageFrame>
      </MemoryRouter>,
    )
    const frame = screen.getByTestId('frame')
    expect(frame).toHaveClass('flex', 'h-full', 'min-h-0', 'flex-col')
    expect(frame).not.toHaveClass('px-[clamp(14px,2.4vw,34px)]', 'pb-[26px]', 'max-nav:pb-4')
    expect(screen.getByRole('banner').querySelector('[data-page-bar]')).toHaveClass('px-4', 'sm:px-6')
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
