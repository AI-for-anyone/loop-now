import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
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

  it('breadcrumb: the earlier levels are links, the last item is the current page, marked in riso blue', () => {
    renderHeader()
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumb' })
    expect(within(crumbs).getByRole('link', { name: 'Demo' })).toHaveAttribute('href', '/?project=Demo')
    const current = within(crumbs).getByText('Tasks')
    expect(current).toHaveAttribute('aria-current', 'page')
    expect(current).toHaveClass('text-riso-blue')
  })

  it('the title is the ZCOOL display face in riso blue with a misregistered fluorescent pink copy; compact on wide screens (40px, same as the Agent page header), up to 60px on narrow screens as before', () => {
    renderHeader()
    const title = screen.getByRole('heading', { level: 1, name: 'Tasks' })
    expect(title).toHaveClass('font-display-cn', 'text-riso-blue', 'text-[clamp(38px,6vw,60px)]', '[text-shadow:4px_4px_0_var(--riso-pink)]')
    expect(title).toHaveClass('nav:text-[40px]', 'nav:leading-none', 'nav:[text-shadow:3px_3px_0_var(--riso-pink)]')
  })

  it('compact header on wide screens: the meta line joins the title row on its right and wraps when it does not fit; tighter breadcrumb; the decoration shrinks to the header height', () => {
    renderHeader(<span>Tasks in this project</span>)
    const header = screen.getByRole('banner')
    expect(header).toHaveClass('nav:gap-1.5', 'nav:pt-3', 'nav:pb-2.5')
    const row = header.querySelector('[data-page-title-row]')!
    expect(row).toContainElement(screen.getByRole('heading', { level: 1 }))
    expect(row).toContainElement(screen.getByText('Tasks in this project'))
    expect(row).toHaveClass('flex-col', 'nav:flex-row', 'nav:flex-wrap', 'nav:items-center', '@[820px]:pr-[316px]', 'nav:@[820px]:pr-[196px]')
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' }).querySelector('ol')).toHaveClass('nav:leading-4')
    expect(header.querySelector('[data-overprint-deco]')).toHaveClass('nav:scale-[0.6]')
  })

  it('the meta line sits below the title; the overprint decoration at the top right is hidden from screen readers and not shown when the header is narrow', () => {
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

  it('page frame: fills the content area directly without outer margins; the header carries its own horizontal gutters', () => {
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
    expect(screen.getByRole('banner')).toHaveClass('px-3.5', 'nav:px-6')
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
