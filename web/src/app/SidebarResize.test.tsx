import { act, fireEvent, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { SIDEBAR_WIDTH_DEFAULT, SIDEBAR_WIDTH_STORAGE_KEY } from '@/lib/sidebar-width'
import { apiUrl, defaultAgentPath, makeAgent } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, sidebar } from '@/test/shell'
import { routes } from './routes'

const originalInnerWidth = window.innerWidth

function setViewportWidth(width: number, notify = true) {
  Object.defineProperty(window, 'innerWidth', { configurable: true, writable: true, value: width })
  if (notify) {
    act(() => {
      window.dispatchEvent(new Event('resize'))
    })
  }
}

function renderShell(viewport = 1440) {
  setViewportWidth(viewport, false)
  const agent = makeAgent({ name: 'loop-now-publicity' })
  server.use(...baseHandlers([agent]))
  // Open an Agent chat URL directly instead of /agents, which redirects asynchronously and would close the mobile drawer.
  return renderRoutes(routes, defaultAgentPath(agent.id, 'chat'))
}

const resizeHandle = () => screen.getByRole('separator', { name: 'Resize sidebar' })
const desktopWrapper = () => resizeHandle().parentElement as HTMLElement

beforeEach(() => {
  setViewportWidth(1440, false)
})

afterEach(() => {
  setViewportWidth(originalInnerWidth, false)
})

describe('desktop sidebar resizing', () => {
  it('starts at the 248px default with vertical separator ARIA bounds on the desktop wrapper', async () => {
    renderShell(1440)
    await screen.findByRole('list', { name: 'Agents' })
    const handle = resizeHandle()
    expect(desktopWrapper()).toHaveStyle({ width: '248px' })
    expect(desktopWrapper()).toHaveClass('max-nav:hidden')
    expect(handle).toHaveAttribute('tabindex', '0')
    expect(handle).toHaveAttribute('aria-orientation', 'vertical')
    expect(handle).toHaveAttribute('aria-valuenow', '248')
    expect(handle).toHaveAttribute('aria-valuemin', '200')
    expect(handle).toHaveAttribute('aria-valuemax', '420')
    expect(handle).toHaveClass('cursor-col-resize')
  })

  it('dragging changes the width live within 200..420px and remembers it on release', async () => {
    renderShell(1440)
    await screen.findByRole('list', { name: 'Agents' })
    const handle = resizeHandle()

    // Non-primary mouse buttons must not start a drag.
    fireEvent.pointerDown(handle, { clientX: 248, button: 2, pointerId: 1 })
    expect(handle).not.toHaveAttribute('data-dragging')

    fireEvent.pointerDown(handle, { clientX: 248, button: 0, pointerId: 1 })
    expect(handle).toHaveAttribute('data-dragging', 'true')
    expect(document.body.style.cursor).toBe('col-resize')
    expect(document.body.style.userSelect).toBe('none')

    fireEvent.pointerMove(window, { clientX: 328, pointerId: 1 })
    expect(desktopWrapper()).toHaveStyle({ width: '328px' })
    expect(handle).toHaveAttribute('aria-valuenow', '328')

    // Exceeding the upper and lower limits clamps cleanly.
    fireEvent.pointerMove(window, { clientX: 700, pointerId: 1 })
    expect(desktopWrapper()).toHaveStyle({ width: '420px' })
    expect(handle).toHaveAttribute('aria-valuenow', '420')

    fireEvent.pointerMove(window, { clientX: 40, pointerId: 1 })
    expect(desktopWrapper()).toHaveStyle({ width: '200px' })
    expect(handle).toHaveAttribute('aria-valuenow', '200')

    fireEvent.pointerMove(window, { clientX: 310, pointerId: 1 })
    fireEvent.pointerUp(window, { clientX: 310, pointerId: 1 })
    expect(handle).not.toHaveAttribute('data-dragging')
    expect(document.body.style.cursor).toBe('')
    expect(document.body.style.userSelect).toBe('')
    expect(desktopWrapper()).toHaveStyle({ width: '310px' })
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY)).toBe('310')
  })

  it('also supports plain mouse events when no pointer event initiates the drag', async () => {
    renderShell(1440)
    await screen.findByRole('list', { name: 'Agents' })
    const handle = resizeHandle()

    fireEvent.mouseDown(handle, { clientX: 248, button: 0 })
    expect(handle).toHaveAttribute('data-dragging', 'true')
    fireEvent.mouseMove(window, { clientX: 340 })
    expect(desktopWrapper()).toHaveStyle({ width: '340px' })
    fireEvent.mouseUp(window, { clientX: 340 })
    expect(handle).not.toHaveAttribute('data-dragging')
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY)).toBe('340')
  })

  it('never exceeds 40% of the window and re-clamps when the window resizes', async () => {
    window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, '400')
    renderShell(920)
    await screen.findByRole('list', { name: 'Agents' })
    const handle = resizeHandle()

    // 40% of 920px is 368px.
    expect(handle).toHaveAttribute('aria-valuemax', '368')
    expect(desktopWrapper()).toHaveStyle({ width: '368px' })
    expect(handle).toHaveAttribute('aria-valuenow', '368')

    // Dragging farther right stays capped at 368px.
    fireEvent.pointerDown(handle, { clientX: 368, button: 0, pointerId: 1 })
    fireEvent.pointerMove(window, { clientX: 520, pointerId: 1 })
    expect(desktopWrapper()).toHaveStyle({ width: '368px' })
    fireEvent.pointerUp(window, { clientX: 520, pointerId: 1 })
  })

  it('keeps the preferred width when a narrow window temporarily clamps it and then widens again', async () => {
    window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, '400')
    renderShell(1440)
    await screen.findByRole('list', { name: 'Agents' })
    expect(desktopWrapper()).toHaveStyle({ width: '400px' })

    setViewportWidth(920)
    expect(desktopWrapper()).toHaveStyle({ width: '368px' })
    expect(resizeHandle()).toHaveAttribute('aria-valuemax', '368')

    setViewportWidth(1440)
    expect(desktopWrapper()).toHaveStyle({ width: '400px' })
    expect(resizeHandle()).toHaveAttribute('aria-valuemax', '420')
  })

  it('restores the remembered width on mount', async () => {
    window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, '356')
    const first = renderShell(1440)
    await screen.findByRole('list', { name: 'Agents' })
    expect(desktopWrapper()).toHaveStyle({ width: '356px' })
    expect(resizeHandle()).toHaveAttribute('aria-valuenow', '356')
    first.unmount()

    renderShell(1440)
    await screen.findByRole('list', { name: 'Agents' })
    expect(desktopWrapper()).toHaveStyle({ width: '356px' })
  })

  it('double-clicking the handle resets to the default width and clears the saved override', async () => {
    window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, '360')
    const user = userEvent.setup()
    const first = renderShell(1440)
    await screen.findByRole('list', { name: 'Agents' })
    expect(desktopWrapper()).toHaveStyle({ width: '360px' })

    await user.dblClick(resizeHandle())
    expect(desktopWrapper()).toHaveStyle({ width: `${SIDEBAR_WIDTH_DEFAULT}px` })
    expect(resizeHandle()).toHaveAttribute('aria-valuenow', String(SIDEBAR_WIDTH_DEFAULT))
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY)).toBeNull()
    first.unmount()

    renderShell(1440)
    await screen.findByRole('list', { name: 'Agents' })
    expect(desktopWrapper()).toHaveStyle({ width: `${SIDEBAR_WIDTH_DEFAULT}px` })
  })

  it('supports keyboard stepping by 16px with ArrowLeft/ArrowRight and jumping to limits with Home/End', async () => {
    const user = userEvent.setup()
    renderShell(1440)
    await screen.findByRole('list', { name: 'Agents' })
    const handle = resizeHandle()
    handle.focus()
    expect(handle).toHaveFocus()

    await user.keyboard('{ArrowRight}')
    expect(desktopWrapper()).toHaveStyle({ width: '264px' })
    expect(handle).toHaveAttribute('aria-valuenow', '264')
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY)).toBe('264')

    await user.keyboard('{ArrowLeft}')
    expect(desktopWrapper()).toHaveStyle({ width: '248px' })
    expect(window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY)).toBe('248')

    await user.keyboard('{Home}')
    expect(desktopWrapper()).toHaveStyle({ width: '200px' })
    expect(handle).toHaveAttribute('aria-valuenow', '200')
    await user.keyboard('{ArrowLeft}')
    expect(desktopWrapper()).toHaveStyle({ width: '200px' })

    await user.keyboard('{End}')
    expect(desktopWrapper()).toHaveStyle({ width: '420px' })
    expect(handle).toHaveAttribute('aria-valuenow', '420')
    await user.keyboard('{ArrowRight}')
    expect(desktopWrapper()).toHaveStyle({ width: '420px' })

    // Modifier combinations such as Meta+ArrowLeft are left to the browser.
    await user.keyboard('{Meta>}{ArrowLeft}{/Meta}')
    expect(desktopWrapper()).toHaveStyle({ width: '420px' })

    // End respects the 40% viewport cap on narrower desktop windows.
    setViewportWidth(960)
    await user.keyboard('{Home}')
    expect(desktopWrapper()).toHaveStyle({ width: '200px' })
    await user.keyboard('{End}')
    expect(desktopWrapper()).toHaveStyle({ width: '384px' })
    expect(handle).toHaveAttribute('aria-valuemax', '384')
  })

  it('falls back to the default and still resizes when localStorage fails', async () => {
    const getSpy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    const setSpy = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    const removeSpy = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('blocked')
    })
    try {
      const user = userEvent.setup()
      renderShell(1440)
      await screen.findByRole('list', { name: 'Agents' })
      const handle = resizeHandle()
      expect(desktopWrapper()).toHaveStyle({ width: '248px' })

      fireEvent.pointerDown(handle, { clientX: 248, button: 0, pointerId: 1 })
      fireEvent.pointerMove(window, { clientX: 300, pointerId: 1 })
      fireEvent.pointerUp(window, { clientX: 300, pointerId: 1 })
      expect(desktopWrapper()).toHaveStyle({ width: '300px' })

      await user.dblClick(handle)
      expect(desktopWrapper()).toHaveStyle({ width: '248px' })
    } finally {
      getSpy.mockRestore()
      setSpy.mockRestore()
      removeSpy.mockRestore()
    }
  })

  it('renders no resize handle inside the narrow phone navigation drawer', async () => {
    const user = userEvent.setup()
    renderShell(390)
    await screen.findByRole('list', { name: 'Agents' })
    await user.click(screen.getByRole('button', { name: 'Open navigation' }))
    const drawer = await screen.findByRole('dialog', { name: 'Main navigation' })
    expect(within(drawer).queryByRole('separator')).toBeNull()
  })

  it('keeps headings, names, labels, marks, badges and the "+" button from clipping at narrow sidebar widths', async () => {
    const agent = makeAgent({
      name: 'loop-now-publicity-with-very-long-name',
      model: { provider: 'claude-code', modelId: 'sonnet' },
      runtime: 'claude-code',
      isManager: true,
    })
    server.use(
      http.get(apiUrl('/inbox/pending'), () =>
        HttpResponse.json({
          questions: [],
          unread: [],
          byAgent: [{ agentId: agent.id, projectId: agent.projectId, questions: 1, unread: 1 }],
          byTask: [],
        }),
      ),
      ...baseHandlers([agent]),
    )
    window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, '200')
    renderRoutes(routes, defaultAgentPath(agent.id, 'chat'))
    const nav = sidebar()
    const list = await within(nav).findByRole('list', { name: 'Agents' })

    // Group heading truncates so the "+" button never shrinks or clips off the right edge.
    const heading = within(nav).getByRole('heading', { name: 'Agents' })
    expect(heading).toHaveClass('min-w-0', 'truncate')
    expect(heading.parentElement).toHaveClass('min-w-0', 'gap-2')
    const createButton = within(nav).getByRole('button', { name: 'New Agent' })
    expect(createButton).toHaveClass('shrink-0')

    // Scope switcher trigger allows its project name to truncate without overflowing or squishing its icons.
    const scopeButton = within(nav).getByRole('button', { name: /^Switch scope/ })
    expect(scopeButton).toHaveClass('min-w-0')
    expect(within(scopeButton).getByText('test-project')).toHaveClass('min-w-0', 'truncate')

    // Agent row name truncates; right-side marks container stays within the row width and wraps if all three marks are shown at 200px.
    const row = await within(list).findByRole('link', { name: /loop-now-publicity-with-very-long-name/ })
    expect(within(row).getByText('loop-now-publicity-with-very-long-name')).toHaveClass('max-w-full', 'truncate')
    const marks = within(row).getByText('Manager').parentElement!
    expect(marks).toHaveClass('ml-auto', 'shrink-0', 'max-w-full', 'flex-wrap')

    // Nav labels truncate, search shortcut does not shrink, and connection status truncates its host inside the sidebar.
    expect(within(within(nav).getByRole('link', { name: 'Project settings' })).getByText('Project settings')).toHaveClass('min-w-0', 'flex-1', 'truncate')
    expect(within(nav).getByRole('searchbox', { name: 'Search Agents' }).closest('label')!.querySelector('kbd')).toHaveClass('shrink-0')
    const status = within(nav).getByRole('status', { name: 'Connection' })
    expect(status).toHaveClass('min-w-0')
    expect(within(status).getByText('127.0.0.1:8780')).toHaveClass('min-w-0', 'truncate')
  })
})
