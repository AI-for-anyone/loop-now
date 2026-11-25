import { screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { apiUrl } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, sidebar } from '@/test/shell'

describe('Draft navigation on the global settings page', () => {
  it('model settings can be edited once loaded, and leaving with unsaved changes asks for confirmation', async () => {
    server.use(
      http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{}}', revision: 1 })),
      ...baseHandlers([]),
    )
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, '/settings')
    const editor = await screen.findByLabelText('models.json')
    await user.keyboard('{Control>}{End}{/Control}')
    await user.type(editor, ' ')
    await user.click(within(sidebar()).getByRole('link', { name: 'Plugin market' }))
    expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/settings')
    expect(editor.textContent?.trim()).toBe('{"providers":{}}')
    expect(editor.textContent).not.toBe('{"providers":{}}')
  })
})

// "Printed sheet" redesign, step 4: global settings sit on a sheet, with numbered sections and the section ruler in the stub.
describe('Global settings on the sheet', () => {
  const titles = ['Language', 'Local mode', 'Agent runtimes', 'Writing with AI', 'Model settings', 'Global folder', 'Server', 'CLI templates']
  const region = (name: string) => screen.getByRole('region', { name })
  const open = async () => {
    server.use(...baseHandlers([]))
    renderRoutes(routes, '/settings')
    return screen.findByRole('heading', { level: 1, name: 'Global settings' })
  }

  it('the header is the page title: breadcrumb “Global›Global settings”, next to it the server version and connection state; what this page can change is written on a yellow sticky note', async () => {
    const title = await open()
    const header = title.closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Global settings')
    expect(await within(header).findByText('Server connected')).toBeInTheDocument()
    const note = screen.getByText('Model settings, local mode and folders can be edited here; server environment configuration remains read-only.').closest('[role="note"]')!
    expect(note).toHaveClass('riso-tilt')
  })

  it('the eight sections are numbered in order and laid out directly on the sheet; the contents ruler in the stub lists the same eight; the local mode section is still at #local-mode', async () => {
    await open()
    await screen.findByRole('region', { name: 'Server' })
    const sheet = region('Language').closest('[data-sheet]')!
    titles.forEach((name, index) => {
      const section = region(name)
      expect(sheet).toContainElement(section)
      expect(section.querySelector('[data-section-number]')).toHaveTextContent(String(index + 1).padStart(2, '0'))
    })
    expect(region('Local mode')).toHaveAttribute('id', 'local-mode')
    const toc = screen.getByRole('navigation', { name: 'Contents' })
    expect(within(toc).getAllByRole('link').map((link) => link.textContent)).toEqual(titles.map((name, index) => `${String(index + 1).padStart(2, '0')}${name}`))
    expect(within(toc).getByRole('link', { name: 'Local mode' })).toHaveAttribute('href', '#local-mode')
  })

  it('the UI language is a segmented button, with the current language in yellow', async () => {
    await open()
    const group = within(region('Language')).getByRole('radiogroup', { name: 'Language' })
    expect(group).toHaveClass('border-2', 'border-ink', 'rounded-full')
    expect(within(group).getByRole('radio', { name: 'English' })).toHaveAttribute('aria-checked', 'true')
    expect(within(group).getByRole('radio', { name: 'English' })).toHaveClass('data-[state=on]:bg-riso-yellow')
  })

  it('the four local mode switches sit one per row with dashed lines between rows; a switch that is on is yellow with a navy knob, and “On” or “Off” is written next to it', async () => {
    await open()
    const local = region('Local mode')
    const switches = await within(local).findAllByRole('switch')
    expect(switches).toHaveLength(4)
    for (const item of switches) {
      expect(item).toHaveClass('data-[state=checked]:bg-riso-yellow', 'border-2', 'border-ink')
      expect(item.closest('li')).toHaveClass('border-b-[1.5px]', 'border-dashed')
      expect(item.nextElementSibling).toHaveTextContent(item.getAttribute('aria-checked') === 'true' ? 'On' : 'Off')
    }
  })

  it('each Agent runtime is a card: 2px navy border, solid navy shadow, name in the display font', async () => {
    await open()
    const card = await within(region('Agent runtimes')).findByRole('region', { name: 'Claude Code' })
    expect(card).toHaveClass('border-2', 'border-ink', 'bg-white', 'shadow-[5px_5px_0_var(--ink)]')
    expect(within(card).getByText('Claude Code')).toHaveClass('font-display-cn')
  })

  it('the global folder section is a description plus a file manager with a 2px navy border, no longer a card', async () => {
    await open()
    const folder = region('Global folder')
    expect(folder.querySelector('[data-file-manager]')).toHaveClass('border-2', 'border-ink')
    expect(folder).not.toHaveClass('shadow-print-rest')
  })

  it('the server section lists the default organization, default project, API address, version and connection state', async () => {
    await open()
    const server = region('Server')
    for (const label of ['Default organization', 'Default project', 'API address', 'Version', 'Connection']) expect(within(server).getByText(label)).toBeInTheDocument()
  })
})
