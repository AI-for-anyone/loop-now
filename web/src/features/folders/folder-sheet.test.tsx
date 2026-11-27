import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { renderWithQuery } from '@/test/render'
import { createMockFolder } from '@/test/folders'
import { t } from '@/i18n'
import { FileManager } from './FileManager'

// Wave 2C redesign: full-bleed 2-pane IDE explorer (no 150px Overview stub, single 40px contextual toolbar, 36px file rows, 28px status strip).
const limits = { maxBytes: 1 << 20, extensions: ['.md', '.go', '.ts', '.json'] }

function setup(preset: 'workspace' | 'project' = 'workspace') {
  const source = createMockFolder({ key: preset, preset, latency: 0, git: preset === 'workspace', gitDuration: 400, uploadDuration: 0 })
  const user = userEvent.setup()
  const view = renderWithQuery(<FileManager variant="sheet" source={source} rootLabel="server's folder" limits={limits} className="h-[600px]" />)
  return { source, user, ...view }
}

const content = () => screen.getByRole('region', { name: 'Files' })

describe('Folder on the sheet', () => {
  it('renders a full-bleed 2-pane IDE explorer without the Overview stub and with a single 40px toolbar of compact 28px controls', async () => {
    const { container } = setup()
    const manager = container.querySelector('[data-file-manager]')!
    expect(manager).not.toHaveClass('rounded-card')
    expect(manager).not.toHaveClass('bg-white')
    expect(manager).toHaveClass('gap-0')
    expect(screen.queryByRole('complementary', { name: 'Overview' })).toBeNull()

    const treeAside = screen.getByRole('complementary', { name: 'File tree' })
    expect(treeAside).toHaveClass('w-[220px]', 'border-r', 'border-hairline', '@min-[40rem]:block')

    const toolbar = container.querySelector('[data-folder-toolbar]')!
    expect(toolbar).toHaveClass('h-10', 'border-b', 'border-hairline')

    for (const name of ['New file', 'New folder', 'Upload', 'Pull from Git']) {
      const button = await within(toolbar as HTMLElement).findByRole('button', { name })
      expect(button).toHaveClass(
        'h-7',
        'rounded-ticket',
        'border',
        'border-hairline-strong',
        'bg-surface-control',
      )
      expect(button).toHaveTextContent(name)
    }

    // Breadcrumb uses a crisp EntryIcon SVG instead of the old 26x22 FolderBlock.
    const nav = screen.getByRole('navigation', { name: 'Location' })
    expect(nav.querySelector('[data-folder-icon]')).toBeNull()
    expect(nav.querySelector('svg')).toHaveClass('size-4')
  })

  it('swaps the single 40px toolbar cleanly between directory actions and file viewing/editing actions without stacking a second bar', async () => {
    const { container, user } = setup()
    await within(content()).findByRole('table')

    const tree = screen.getByRole('tree')
    const readme = tree.querySelector<HTMLElement>('[role=treeitem][data-path="README.md"]')!
    await user.click(readme)

    const toolbar = container.querySelector<HTMLElement>('[data-folder-toolbar]')!
    expect(within(toolbar).queryByRole('button', { name: 'New file' })).toBeNull()
    expect(await within(toolbar).findByRole('radiogroup', { name: 'View' })).toBeInTheDocument()
    expect(within(toolbar).getByRole('link', { name: 'Open in new tab' })).toHaveClass('h-7')
    expect(within(toolbar).getByRole('button', { name: 'Edit' })).toHaveClass('h-7')
    expect(within(toolbar).getByRole('button', { name: 'Download' })).toHaveClass('h-7')
    // No duplicate file header toolbar inside the content pane.
    expect(within(content()).queryByRole('button', { name: 'Edit' })).toBeNull()
    expect(within(content()).queryByRole('radiogroup', { name: 'View' })).toBeNull()

    await user.click(within(toolbar).getByRole('button', { name: 'Edit' }))
    expect(await within(toolbar).findByRole('button', { name: 'Save' })).toHaveClass('h-7')
    expect(within(toolbar).getByRole('button', { name: 'Cancel' })).toHaveClass('h-7')
    expect(toolbar).toHaveClass('bg-tint-yellow/50')
  })

  it('no AGENTS.md yet: a compact single-row banner with unified surface styling', async () => {
    setup('project')
    const note = await screen.findByRole('note')
    expect(note).toHaveTextContent('No AGENTS.md yet')
    expect(note).toHaveClass(
      'px-3',
      'py-2',
      'border',
      'border-hairline-strong',
      'bg-surface-subtle',
      'shadow-surface',
    )
  })

  it('file list: high-density 36px rows with 16px EntryIcon, no redundant heading, and crisp hairline rows', async () => {
    setup()
    const table = await within(content()).findByRole('table')
    expect(within(content()).queryByRole('heading', { level: 3, name: "server's folder" })).toBeNull()
    expect(table.querySelector('thead tr')).toHaveClass('border-b', 'border-hairline-strong')
    const rows = [...table.querySelectorAll('tbody tr')]
    expect(rows.length).toBeGreaterThan(2)
    for (const row of rows) {
      expect(row).toHaveClass('h-9', 'border-b', 'border-hairline')
      expect(row.querySelector('[data-folder-icon]')).toBeNull()
      expect(row.querySelector('[data-file-icon]')).toBeNull()
      expect(row.querySelector('svg')).toHaveClass('size-4')
    }
    const cells = rows[0].querySelectorAll('td')
    expect(cells[1]).toHaveClass('font-mono')
    expect(cells[2]).toHaveClass('font-mono')
  })

  it('the selected item in the file tree uses surface-active token', async () => {
    const { user } = setup()
    const tree = await screen.findByRole('tree')
    // The root folder is selected at first.
    const root = within(tree).getAllByRole('treeitem')[0]
    expect(root).toHaveAttribute('aria-selected', 'true')
    expect(root).toHaveClass('bg-surface-active')
    // After opening another folder, the selection styling follows it.
    const web = () => tree.querySelector<HTMLElement>('[role=treeitem][data-path="web"]')
    await waitFor(() => expect(web()).not.toBeNull())
    await user.click(web()!)
    await waitFor(() => expect(web()).toHaveAttribute('aria-selected', 'true'))
    expect(web()).toHaveClass('bg-surface-active')
    expect(root).not.toHaveClass('bg-surface-active')
  })

  it('bottom 28px status strip shows live directory counts and size, switches to file metadata in file mode, and keeps the concurrency tooltip', async () => {
    const { container, source, user } = setup()
    const { entries } = await source.list('', null)
    const dirs = entries.filter((entry) => entry.type === 'dir').length
    const nonDirs = entries.length - dirs
    const bytes = entries.reduce((sum, entry) => sum + (entry.size ?? 0), 0)

    await within(content()).findByRole('table')
    const footer = container.querySelector<HTMLElement>('[data-folder-footer]')!
    expect(footer).toHaveClass('h-7', 'border-t', 'border-hairline', 'bg-surface-subtle/50')
    expect(footer).toHaveTextContent(`${t.folders.summary(dirs, nonDirs)} · ${t.folders.size(bytes)}`)
    expect(footer.querySelector(`[title*="${t.folders.concurrent}"]`)).not.toBeNull()

    // Selecting a file updates the status strip to file metadata.
    const goMod = screen.getByRole('tree').querySelector<HTMLElement>('[role=treeitem][data-path="go.mod"]')!
    await user.click(goMod)
    const goEntry = entries.find((entry) => entry.name === 'go.mod')!
    await waitFor(() => expect(footer).toHaveTextContent(t.folders.size(goEntry.size!)))
    expect(footer).not.toHaveTextContent(t.folders.summary(dirs, nonDirs))
  })

  it('the card and panel variants keep a card border and no Overview stub', async () => {
    const source = createMockFolder({ key: 'card', preset: 'project', latency: 0 })
    const { container, rerender } = renderWithQuery(<FileManager source={source} rootLabel="Project folder" limits={limits} className="h-[600px]" />)
    await screen.findByRole('note')
    expect(container.querySelector('[data-file-manager]')).toHaveClass('rounded-card')
    expect(screen.queryByRole('complementary', { name: 'Overview' })).toBeNull()

    rerender(<FileManager variant="panel" source={source} rootLabel="Project folder" limits={limits} className="h-[600px]" />)
    expect(container.querySelector('[data-file-manager]')).toHaveClass('rounded-card', 'border-hairline-strong')
    expect(screen.queryByRole('complementary', { name: 'Overview' })).toBeNull()
  })
})
