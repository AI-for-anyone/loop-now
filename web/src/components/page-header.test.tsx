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

  it('breadcrumb: the earlier levels are links, the last item is the current page in primary ink (riso blue in Plate skin)', () => {
    renderHeader()
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumb' })
    expect(within(crumbs).getByRole('link', { name: 'Demo' })).toHaveAttribute('href', '/?project=Demo')
    const current = within(crumbs).getByText('Tasks')
    expect(current).toHaveAttribute('aria-current', 'page')
    expect(current).toHaveClass('text-ink', '[[data-skin=plate]_&]:text-riso-blue')
  })

  it('the title uses compact semibold mono typography by default and preserves ZCOOL display styling with pink misregistration in Plate skin', () => {
    renderHeader()
    const title = screen.getByRole('heading', { level: 1, name: 'Tasks' })
    expect(title).toHaveClass('text-[22px]', 'font-semibold', 'tracking-tight', 'text-ink')
    expect(title).toHaveClass(
      '[[data-skin=plate]_&]:font-display-cn',
      '[[data-skin=plate]_&]:text-riso-blue',
      '[[data-skin=plate]_&]:text-[clamp(38px,6vw,60px)]',
      '[[data-skin=plate]_&]:[text-shadow:4px_4px_0_var(--riso-pink)]',
      '[[data-skin=plate]_&]:nav:text-[40px]',
      '[[data-skin=plate]_&]:nav:[text-shadow:3px_3px_0_var(--riso-pink)]',
    )
  })

  it('compact header on wide screens: meta joins the title row; right padding for OverprintDeco is scoped to Plate skin only', () => {
    renderHeader(<span>Tasks in this project</span>)
    const header = screen.getByRole('banner')
    expect(header).toHaveClass('nav:gap-1.5', 'nav:pt-3.5', 'nav:pb-3')
    const row = header.querySelector('[data-page-title-row]')!
    expect(row).toContainElement(screen.getByRole('heading', { level: 1 }))
    expect(row).toContainElement(screen.getByText('Tasks in this project'))
    expect(row).toHaveClass(
      'flex-col',
      'nav:flex-row',
      'nav:flex-wrap',
      'nav:items-center',
      '[[data-skin=plate]_&]:@[820px]:pr-[316px]',
      '[[data-skin=plate]_&]:nav:@[820px]:pr-[196px]',
    )
    expect(row).not.toHaveClass('@[820px]:pr-[316px]', 'nav:@[820px]:pr-[196px]')
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

  it('page frame: side padding follows the window width, with extra bottom padding reserved for Plate skin shadow', () => {
    render(<PageFrame data-testid="frame">Body</PageFrame>)
    expect(screen.getByTestId('frame')).toHaveClass(
      'flex',
      'h-full',
      'min-h-0',
      'flex-col',
      'px-[clamp(14px,2.4vw,34px)]',
      'pb-4',
      '[[data-skin=plate]_&]:pb-[26px]',
    )
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
