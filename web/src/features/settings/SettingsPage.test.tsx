import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { apiUrl } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, sidebar } from '@/test/shell'

describe('Draft navigation on the global settings page', () => {
  it('model settings can be edited once loaded, and leaving or switching tabs with unsaved changes asks for confirmation', async () => {
    server.use(
      http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{}}', revision: 1 })),
      ...baseHandlers([]),
    )
    const user = userEvent.setup()
    const { router } = renderRoutes(routes, '/settings/runtimes#settings-models')
    const editor = await screen.findByLabelText('models.json')
    await user.keyboard('{Control>}{End}{/Control}')
    await user.type(editor, ' ')

    // Switching SubNav tabs while dirty triggers the navigation guard.
    await user.click(screen.getByRole('link', { name: 'Workspace & Local Mode' }))
    const tabDialog = await screen.findByRole('alertdialog')
    await user.click(within(tabDialog).getByRole('button', { name: 'Cancel' }))
    expect(router.state.location.pathname).toBe('/settings/runtimes')

    // Leaving Global settings while dirty also triggers the navigation guard.
    await user.click(within(sidebar()).getByRole('link', { name: 'Plugin market' }))
    expect(await screen.findByRole('alertdialog')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/settings/runtimes')
    expect(editor.textContent?.trim()).toBe('{"providers":{}}')
    expect(editor.textContent).not.toBe('{"providers":{}}')
  })
})

describe('Global settings tabs and routing', () => {
  const region = (name: string) => screen.getByRole('region', { name })
  const open = async (path = '/settings') => {
    server.use(...baseHandlers([]))
    const view = renderRoutes(routes, path)
    const title = await screen.findByRole('heading', { level: 1, name: 'Global settings' })
    return { ...view, title }
  }

  it('the header shows breadcrumb, server connection state, and 4 URL-backed SubNav tabs without the legacy SectionToc stub', async () => {
    const { title } = await open()
    const header = title.closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('Global›Global settings')
    expect(await within(header).findByText('Server connected')).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: 'Contents' })).toBeNull()

    const nav = screen.getByRole('navigation', { name: 'Global settings sections' })
    expect(within(nav).getAllByRole('link').map((link) => link.textContent)).toEqual([
      'General & Appearance',
      'Runtimes & Models',
      'Workspace & Local Mode',
      'Server & Diagnostics',
    ])
  })

  it.each([
    ['/settings#local-mode', '/settings/workspace', '#local-mode'],
    ['/settings#settings-folder', '/settings/workspace', '#settings-folder'],
    ['/settings#settings-runtimes', '/settings/runtimes', '#settings-runtimes'],
    ['/settings#settings-writing', '/settings/runtimes', '#settings-writing'],
    ['/settings#settings-models', '/settings/runtimes', '#settings-models'],
    ['/settings#settings-server', '/settings/server', '#settings-server'],
    ['/settings#settings-clis', '/settings/server', '#settings-cli-templates'],
  ])('redirects legacy hash %s to %s%s', async (from, expectedPath, expectedHash) => {
    const { router } = await open(from)
    await waitFor(() => expect(router.state.location.pathname).toBe(expectedPath))
    expect(router.state.location.hash).toBe(expectedHash)
  })

  it('the UI language is a segmented button using semantic segment tokens', async () => {
    await open()
    const group = within(region('Language')).getByRole('radiogroup', { name: 'Language' })
    expect(group).toHaveClass('border', 'border-hairline', 'bg-segment-track')
    expect(within(group).getByRole('radio', { name: 'English' })).toHaveAttribute('aria-checked', 'true')
    expect(within(group).getByRole('radio', { name: 'English' })).toHaveClass('data-[state=on]:bg-segment-active')
  })

  it('switching theme between Risograph (Classic) and Minimal updates document.documentElement.dataset.skin', async () => {
    const user = userEvent.setup()
    await open()
    const appearance = region('Appearance')
    await user.click(within(appearance).getByRole('radio', { name: /Risograph \(Classic\)/ }))
    expect(document.documentElement.dataset.skin).toBe('plate')
    await user.click(within(appearance).getByRole('radio', { name: /Minimal/ }))
    expect(document.documentElement.dataset.skin).toBe('mono')
  })

  it('Workspace & Local Mode switches between Local mode and full-bleed Global folder', async () => {
    const user = userEvent.setup()
    await open('/settings/workspace')
    const local = region('Local mode')
    expect(local).toHaveAttribute('id', 'local-mode')
    const switches = await within(local).findAllByRole('switch')
    expect(switches).toHaveLength(4)
    for (const item of switches) {
      expect(item).toHaveClass('bg-switch-track', 'data-[state=checked]:bg-cta-bg')
      expect(item.closest('li')).toHaveClass('border-b', 'border-hairline')
      expect(item.nextElementSibling).toHaveTextContent(item.getAttribute('aria-checked') === 'true' ? 'On' : 'Off')
    }

    const viewSwitch = screen.getByRole('radiogroup', { name: 'Workspace & Local Mode view' })
    await user.click(within(viewSwitch).getByRole('radio', { name: 'Global Folder' }))
    const folder = region('Global folder')
    expect(folder.querySelector('[data-file-manager]')).not.toBeNull()
    expect(folder).not.toHaveClass('shadow-print-rest')
  })

  it('Runtimes & Models renders runtime cards and switches to full-height models.json editor', async () => {
    server.use(http.get(apiUrl('/model-settings'), () => HttpResponse.json({ modelsJson: '{"providers":{}}', revision: 1 })))
    const user = userEvent.setup()
    await open('/settings/runtimes')
    const card = await within(region('Agent runtimes')).findByRole('region', { name: 'Claude Code' })
    expect(card).toHaveClass('border', 'border-hairline-strong', 'bg-surface-control', 'shadow-surface')
    expect(within(card).getByText('Claude Code')).toHaveClass('font-display-cn')

    const viewSwitch = screen.getByRole('radiogroup', { name: 'Runtimes & Models view' })
    await user.click(within(viewSwitch).getByRole('radio', { name: 'models.json' }))
    expect(await screen.findByLabelText('models.json')).toBeInTheDocument()
  })

  it('Server & Diagnostics shows the relocated read-only note, server details, and CLI templates', async () => {
    await open('/settings/server')
    const note = screen.getByText('Model settings, local mode and folders can be edited here; server environment configuration remains read-only.').closest('[role="note"]')!
    expect(note).toHaveClass('rounded-card', 'border')
    const serverSection = region('Server')
    for (const label of ['Default organization', 'Default project', 'API address', 'Version', 'Connection']) {
      expect(within(serverSection).getByText(label)).toBeInTheDocument()
    }
    expect(region('CLI templates')).toBeInTheDocument()
  })
})
