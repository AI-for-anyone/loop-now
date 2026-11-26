import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { renderWithQuery } from '@/test/render'
import { createMockFolder } from '@/test/folders'
import { t } from '@/i18n'
import { FileManager } from './FileManager'

// "Printed sheet" redesign, step 2: the Agent's Folder tab and the project folder page sit on a sheet (variant="sheet").
const limits = { maxBytes: 1 << 20, extensions: ['.md', '.go', '.ts', '.json'] }

function setup(preset: 'workspace' | 'project' = 'workspace') {
  const source = createMockFolder({ key: preset, preset, latency: 0, git: preset === 'workspace', gitDuration: 400, uploadDuration: 0 })
  const user = userEvent.setup()
  const view = renderWithQuery(<FileManager variant="sheet" source={source} rootLabel="server's folder" limits={limits} className="h-[600px]" />)
  return { source, user, ...view }
}

const content = () => screen.getByRole('region', { name: 'Files' })

describe('Folder on the sheet', () => {
  it('sits directly on the sheet without a card around it; the toolbar is at the top: New, Upload and Pull from Git use control styling in mono skin and navy-outlined pills in Plate skin', async () => {
    const { container } = setup()
    const manager = container.querySelector('[data-file-manager]')!
    expect(manager).not.toHaveClass('rounded-card')
    expect(manager).not.toHaveClass('bg-white')
    for (const name of ['New file', 'New folder', 'Upload', 'Pull from Git']) {
      const button = await screen.findByRole('button', { name })
      expect(button).toHaveClass(
        'h-[34px]',
        'rounded-ticket',
        'border',
        'border-hairline-strong',
        'bg-surface-control',
        '[[data-skin=plate]_&]:rounded-full',
        '[[data-skin=plate]_&]:border-2',
        '[[data-skin=plate]_&]:border-ink',
        '[[data-skin=plate]_&]:bg-paper-raised',
      )
      expect(button).toHaveTextContent(name)
    }
    // The root folder's name is preceded by a folder block (yellow in Plate skin).
    expect(screen.getByRole('navigation', { name: 'Location' }).querySelector('[data-folder-icon]')).toHaveClass(
      'bg-surface-active',
      'border',
      'border-hairline-strong',
      '[[data-skin=plate]_&]:bg-riso-yellow',
      '[[data-skin=plate]_&]:border-2',
      '[[data-skin=plate]_&]:border-ink',
    )
  })

  it('no AGENTS.md yet: a subtle bordered callout in mono skin and a pink-shadowed card in Plate skin', async () => {
    setup('project')
    const note = await screen.findByRole('note')
    expect(note).toHaveTextContent('No AGENTS.md yet')
    expect(note).toHaveClass(
      'border',
      'border-hairline-strong',
      'bg-surface-subtle',
      'shadow-surface',
      '[[data-skin=plate]_&]:bg-tint-pink-2',
      '[[data-skin=plate]_&]:border-2',
      '[[data-skin=plate]_&]:border-ink',
      '[[data-skin=plate]_&]:shadow-print-pink',
    )
  })

  it('file list: a solid line under the header, dashed lines between rows; folder and file icons adapt to mono and Plate skins; size and time are monospaced', async () => {
    setup()
    const table = await within(content()).findByRole('table')
    expect(table.querySelector('thead tr')).toHaveClass('border-b-2', 'border-ink')
    const rows = [...table.querySelectorAll('tbody tr')]
    expect(rows.length).toBeGreaterThan(2)
    for (const row of rows) expect(row).toHaveClass('border-b-[1.5px]', 'border-dashed')
    const dir = rows.find((row) => row.querySelector('[data-folder-icon]'))!
    const file = rows.find((row) => row.querySelector('[data-file-icon]'))!
    expect(dir.querySelector('[data-folder-icon]')).toHaveClass('bg-surface-active', '[[data-skin=plate]_&]:bg-riso-yellow')
    expect(file.querySelector('[data-file-icon]')).toHaveClass('bg-surface-control', 'border', 'border-hairline-strong', '[[data-skin=plate]_&]:bg-white', '[[data-skin=plate]_&]:border-2', '[[data-skin=plate]_&]:border-ink')
    const cells = file.querySelectorAll('td')
    expect(cells[1]).toHaveClass('font-mono')
    expect(cells[2]).toHaveClass('font-mono')
  })

  it('the selected item in the file tree is yellow', async () => {
    const { user } = setup()
    const tree = await screen.findByRole('tree')
    // The root folder is selected at first.
    const root = within(tree).getAllByRole('treeitem')[0]
    expect(root).toHaveAttribute('aria-selected', 'true')
    expect(root).toHaveClass('bg-riso-yellow')
    // After opening another folder, the yellow follows it.
    const web = () => tree.querySelector<HTMLElement>('[role=treeitem][data-path="web"]')
    await waitFor(() => expect(web()).not.toBeNull())
    await user.click(web()!)
    await waitFor(() => expect(web()).toHaveAttribute('aria-selected', 'true'))
    expect(web()).toHaveClass('bg-riso-yellow')
    expect(root).not.toHaveClass('bg-riso-yellow')
  })

  it('a dashed line at the bottom with a note below it', async () => {
    const { container } = setup()
    await within(content()).findByRole('table')
    const footer = container.querySelector('[data-folder-footer]')!
    expect(footer).toHaveClass('border-t-2', 'border-dashed')
    expect(footer).toHaveTextContent('The Agent may be editing these files too; the last save wins.')
  })

  it('the “Overview” stub shows the number of folders, the number of files and the total file size of the current folder; hidden on narrow screens', async () => {
    const { source } = setup()
    const { entries } = await source.list('', null)
    const dirs = entries.filter((entry) => entry.type === 'dir').length
    const files = entries.filter((entry) => entry.type === 'file')
    const bytes = files.reduce((sum, entry) => sum + (entry.size ?? 0), 0)
    const stub = await screen.findByRole('complementary', { name: 'Overview' })
    expect(stub).toHaveClass('max-nav:hidden', 'border-l', 'border-hairline', '[[data-skin=plate]_&]:border-dashed')
    await waitFor(() => expect(within(stub).getByText('folders')).toBeInTheDocument())
    await waitFor(() => expect(within(stub).getByText('folders').previousElementSibling).toHaveTextContent(String(dirs)))
    expect(within(stub).getByText('files').previousElementSibling).toHaveTextContent(String(files.length))
    const [value, unit] = t.folders.size(bytes).split(' ')
    expect(within(stub).getByText(unit).previousElementSibling).toHaveTextContent(value)
  })

  it('the card version (shared folders on settings pages) is unchanged: it has a border and no stub', async () => {
    const source = createMockFolder({ key: 'card', preset: 'project', latency: 0 })
    const { container } = renderWithQuery(<FileManager source={source} rootLabel="Project folder" limits={limits} className="h-[600px]" />)
    await screen.findByRole('note')
    expect(container.querySelector('[data-file-manager]')).toHaveClass('rounded-card')
    expect(screen.queryByRole('complementary', { name: 'Overview' })).toBeNull()
  })
})
