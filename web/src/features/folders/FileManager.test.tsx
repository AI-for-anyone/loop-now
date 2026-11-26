import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { createTestQueryClient, renderWithQuery } from '@/test/render'
import { createMockFolder } from '@/test/folders'
import { FileManager } from './FileManager'
import type { FolderSource } from './folder-source'

const limits = { maxBytes: 1 << 20, extensions: ['.md', '.go', '.ts', '.json'] }

function setup(preset: 'workspace' | 'project' = 'workspace', options: { maxUpload?: number; source?: FolderSource } = {}) {
  const source = options.source ?? createMockFolder({ key: preset, preset, latency: 0, git: preset === 'workspace', gitDuration: 400, uploadDuration: 0 })
  const user = userEvent.setup()
  const view = renderWithQuery(<FileManager source={source} rootLabel="server's folder" limits={limits} maxUpload={options.maxUpload} className="h-[600px]" />)
  return { source, user, ...view }
}
const tree = () => screen.getByRole('tree')
const row = (path: string) => tree().querySelector<HTMLElement>(`[data-path="${path}"]`)
const findRow = (path: string) => waitFor(() => { const el = row(path); if (!el) throw new Error(`no row ${path}`); return el })
const menu = async (user: ReturnType<typeof userEvent.setup>, path: string, item: string) => {
  await user.click(within(await findRow(path)).getByRole('button', { name: /More actions/ }))
  await user.click(await screen.findByRole('menuitem', { name: item }))
}

describe('File manager', () => {
  it('reads only expanded folders; the root AGENTS.md comes first, is marked as the entry and is shown like a README; large folders are read page by page', async () => {
    const { source, user } = setup()
    const list = vi.spyOn(source, 'list')
    const entry = await findRow('AGENTS.md')
    expect(tree().querySelectorAll('[role=treeitem]')[1]).toBe(entry)
    expect(within(entry).getByText('Entry')).toBeInTheDocument()
    expect(await within(screen.getByRole('region', { name: 'AGENTS.md' })).findByText('You develop and maintain the Loop Now server (Go).')).toBeInTheDocument()
    expect(list.mock.calls.map(([path]) => path)).toEqual([])

    await user.click(await findRow('web'))
    await user.click(await findRow('web/node_modules'))
    await findRow('web/node_modules/@babel')
    expect(list.mock.calls.map(([path, cursor]) => [path, cursor])).toEqual([['web', null], ['web/node_modules', null]])
    const deep = () => tree().querySelectorAll('[role=treeitem][aria-level="4"][data-path]').length
    expect(deep()).toBe(100)
    await user.click(within(tree()).getByText('Show more'))
    await waitFor(() => expect(deep()).toBe(186))
    expect(within(tree()).queryByText('Show more')).toBeNull()
  })

  it('opens a file to view it and edits it in place: leaving with unsaved changes asks for confirmation; saving writes the BOM and CRLF back as they were', async () => {
    const source = createMockFolder({ key: 'w', preset: 'workspace', latency: 0 })
    await source.write('crlf.md', '﻿# 记录\r\n- 一\r\n', true) // check-english: zh (BOM and CRLF round trip of non-ASCII text)
    const write = vi.spyOn(source, 'write')
    const { user } = setup('workspace', { source })
    await user.click(await findRow('crlf.md'))
    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    const editor = await screen.findByRole('textbox', { name: 'crlf.md' })
    expect(editor).toHaveValue('# 记录\n- 一\n') // check-english: zh (BOM and CRLF round trip of non-ASCII text)
    await user.type(editor, '- 二') // check-english: zh (BOM and CRLF round trip of non-ASCII text)
    expect(screen.getByText('Unsaved')).toBeInTheDocument()

    await user.click(await findRow('go.mod'))
    const confirm = await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' })
    await user.click(within(confirm).getByRole('button', { name: 'Cancel' }))
    expect(editor).toHaveValue('# 记录\n- 一\n- 二') // check-english: zh (BOM and CRLF round trip of non-ASCII text)

    await user.click(editor)
    await user.keyboard('{Meta>}s{/Meta}')
    await waitFor(() => expect(write).toHaveBeenCalledWith('crlf.md', '﻿# 记录\r\n- 一\r\n- 二', false)) // check-english: zh (BOM and CRLF round trip of non-ASCII text)
    await waitFor(() => expect(screen.queryByText('Unsaved')).toBeNull())
    expect(screen.queryByRole('textbox', { name: 'crlf.md' })).toBeNull()
  })

  it('creates and renames in place, confirms before deleting, moves to another folder', async () => {
    const { source, user } = setup()
    const write = vi.spyOn(source, 'write'), move = vi.spyOn(source, 'move'), remove = vi.spyOn(source, 'remove')
    await findRow('AGENTS.md')

    await user.click(screen.getByRole('button', { name: 'New file' }))
    await user.type(within(tree()).getByRole('textbox', { name: 'File name' }), 'plan.md{Enter}')
    await waitFor(() => expect(write).toHaveBeenCalledWith('plan.md', '', true))
    expect(await screen.findByRole('textbox', { name: 'plan.md' })).toHaveValue('')
    await user.click(screen.getByRole('button', { name: 'Cancel' }))

    await menu(user, 'README.md', 'Rename')
    const name = within(tree()).getByRole('textbox', { name: 'File name' })
    expect(name).toHaveValue('README.md')
    await user.clear(name)
    await user.type(name, 'OVERVIEW.md{Enter}')
    await waitFor(() => expect(move).toHaveBeenCalledWith('README.md', 'OVERVIEW.md'))
    await findRow('OVERVIEW.md')

    await menu(user, 'docs', 'Delete')
    const confirm = await screen.findByRole('alertdialog', { name: 'Delete "docs"?' })
    expect(confirm).toHaveTextContent('Everything in the folder is deleted too. This can\'t be undone.')
    await user.click(within(confirm).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(remove).toHaveBeenCalledWith('docs'))
    await waitFor(() => expect(row('docs')).toBeNull())

    await menu(user, 'Makefile', 'Move to…')
    const dialog = await screen.findByRole('dialog', { name: 'Move "Makefile"' })
    await user.type(within(dialog).getByLabelText('Move to folder'), 'scripts')
    await user.click(within(dialog).getByRole('button', { name: 'Move' }))
    await waitFor(() => expect(move).toHaveBeenCalledWith('Makefile', 'scripts/Makefile'))
    await waitFor(() => expect(row('Makefile')).toBeNull())
  })

  it('uploads into the selected folder; a file over the per-file limit is not sent and is marked as failed right away', async () => {
    const { source, user, container } = setup('workspace', { maxUpload: 100 })
    const upload = vi.spyOn(source, 'upload')
    await user.click(await findRow('handoff'))
    await user.click(screen.getByRole('button', { name: 'Upload' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Upload files' }))
    await user.upload(container.querySelector<HTMLInputElement>('input[type=file]:not([webkitdirectory])')!, [new File(['# Test results\n'], 'report.md', { type: 'text/markdown' }), new File([new Uint8Array(500)], 'big.bin')])
    const panel = await screen.findByRole('region', { name: 'Upload' })
    await waitFor(() => expect(within(panel).getByRole('status')).toHaveTextContent('Uploaded 1 file'))
    expect(within(panel).getByText('1 failed')).toBeInTheDocument()
    expect(within(panel).getByTitle('Over the per-file limit (100 B)')).toHaveTextContent('handoff/big.bin')
    expect(upload).toHaveBeenCalledTimes(1)
    expect(upload.mock.calls[0].slice(0, 2)).toEqual(['handoff', expect.objectContaining({ path: 'report.md' })])
    await findRow('handoff/report.md')
  })

  it('without an AGENTS.md at the root explains what it is for and opens it for editing after one-click creation; shared folders have no “Pull from Git”', async () => {
    const { source, user } = setup('project')
    const write = vi.spyOn(source, 'write')
    const note = await screen.findByRole('note')
    expect(note).toHaveTextContent('No AGENTS.md yet')
    expect(screen.queryByRole('button', { name: 'Pull from Git' })).toBeNull()
    await user.click(within(note).getByRole('button', { name: 'Create AGENTS.md' }))
    await waitFor(() => expect(write).toHaveBeenCalledWith('AGENTS.md', expect.stringContaining('# Working notes'), true))
    expect((await screen.findByRole<HTMLTextAreaElement>('textbox', { name: 'AGENTS.md' })).value).toContain('The Agent reads this file first every time it starts')
  })

  // Each of the two pulls waits for one 1.5-second polling round; run alone the test takes
  // about 4 seconds, and with the full suite in parallel it exceeds the default 5 seconds.
  it('pulls from Git: progress and success show in the status bar; a failed pull is still there after a reload and its git output can be viewed', async () => {
    const { source, user, unmount } = setup()
    const start = vi.spyOn(source.git!, 'start')
    await findRow('AGENTS.md')
    await user.click(screen.getByRole('button', { name: 'Pull from Git' }))
    let dialog = await screen.findByRole('dialog', { name: 'Pull from Git' })
    await user.type(within(dialog).getByLabelText('Repository URL'), 'https://github.com/acme/budget-ledger.git')
    expect(within(dialog).getByLabelText('Into')).toHaveValue('budget-ledger')
    await user.click(within(dialog).getByRole('button', { name: 'Start pull' }))
    expect(start).toHaveBeenCalledWith({ url: 'https://github.com/acme/budget-ledger.git', branch: 'main', path: 'budget-ledger' })
    expect(await screen.findByText('Pulling https://github.com/acme/budget-ledger.git → budget-ledger')).toBeInTheDocument()
    expect(await screen.findByText('Pulled into budget-ledger', undefined, { timeout: 4000 })).toBeInTheDocument()
    await findRow('budget-ledger')

    await user.click(screen.getByRole('button', { name: 'Pull from Git' }))
    dialog = await screen.findByRole('dialog', { name: 'Pull from Git' })
    await user.type(within(dialog).getByLabelText('Repository URL'), 'https://github.com/acme/private-fail.git')
    await user.click(within(dialog).getByRole('button', { name: 'Start pull' }))
    expect(await screen.findByText('Pull failed: Couldn\'t clone the repository', undefined, { timeout: 4000 })).toBeInTheDocument()

    // Reload the page: the most recent failed pull is still shown, and its git output can be viewed.
    unmount()
    renderWithQuery(<FileManager source={source} rootLabel="server's folder" limits={limits} />, createTestQueryClient())
    expect(await screen.findByText('Pull failed: Couldn\'t clone the repository')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'View git output' }))
    expect(screen.getByText(/could not read Username/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Close' }))
    expect(screen.queryByText('Pull failed: Couldn\'t clone the repository')).toBeNull()
  }, 15_000)

  it('binary files and files over the limit can only be downloaded; a symlink shows its target and opens the target\'s content', async () => {
    const { user } = setup()
    await user.click(await findRow('data'))
    await user.click(await findRow('data/fixtures.bin'))
    expect(await screen.findByText('Binary file; can\'t be previewed.')).toBeInTheDocument()
    await user.click(await findRow('logs'))
    await user.click(await findRow('logs/server.log'))
    expect(await screen.findByText('File is over 1.0 MiB; it can\'t be previewed or edited online.')).toBeInTheDocument()
    await user.click(await findRow('current-notes.md'))
    expect(await screen.findByText('Symlink to handoff/notes.md')).toBeInTheDocument()
    expect(await screen.findByText('The budget ledger write order is done; the tests still need a run.')).toBeInTheDocument()
  })

  it('with the default 20 MiB limit, previews a 3.7 MiB image and a 5 MiB text file and saves it after an edit; a 21 MiB file can only be downloaded', async () => {
    const source = createMockFolder({ key: 'w', preset: 'workspace', latency: 0 })
    await source.write('shots/large.png', new Blob([new Uint8Array(3_700_000)], { type: 'image/png' }), true)
    await source.write('shots/huge.png', new Blob([new Uint8Array(21 << 20)], { type: 'image/png' }), true)
    // About 5,000 lines of 1 KiB: the test DOM is slow at many lines, a real browser much less so.
    const line = 'A line of a large text file, long enough to count. '.repeat(20) + '\n'
    const big = line.repeat(Math.ceil((5 << 20) / line.length))
    await source.write('big.txt', big, true)
    const write = vi.spyOn(source, 'write')
    const user = userEvent.setup()
    renderWithQuery(<FileManager source={source} rootLabel="server's folder" limits={{ maxBytes: 20 << 20, extensions: ['.txt'] }} className="h-[600px]" />)

    await user.click(await findRow('shots'))
    await user.click(await findRow('shots/large.png'))
    expect(await screen.findByRole('img', { name: 'large.png' })).toHaveAttribute('src', source.contentURL('shots/large.png'))
    await user.click(await findRow('shots/huge.png'))
    expect(await screen.findByText('File is over 20.0 MiB; it can\'t be previewed or edited online.')).toBeInTheDocument()
    expect(screen.queryByRole('img', { name: 'huge.png' })).toBeNull()

    await user.click(await findRow('big.txt'))
    await user.click(await screen.findByRole('button', { name: 'Edit' }))
    const editor = await screen.findByRole('textbox', { name: 'big.txt' })
    expect((editor as HTMLTextAreaElement).value).toHaveLength(big.length)
    fireEvent.change(editor, { target: { value: big + 'One more line.\n' } })
    await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(write).toHaveBeenCalledWith('big.txt', big + 'One more line.\n', false))
    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'big.txt' })).toBeNull())
  }, 30_000)

  it('symlink pointing outside the folder: when the server refuses to read it, says why instead of showing the generic validation error', async () => {
    const source = createMockFolder({ key: 'w', preset: 'workspace', latency: 0 })
    const list = source.list.bind(source), read = source.read.bind(source)
    const outside = { name: 'outside.txt', path: 'outside.txt', type: 'symlink' as const, size: null, modifiedAt: '2026-10-25T08:00:00Z', symlinkTarget: '/etc/hosts' }
    vi.spyOn(source, 'list').mockImplementation(async (path, cursor) => { const page = await list(path, cursor); return path === '' ? { ...page, entries: [...page.entries, outside] } : page })
    vi.spyOn(source, 'read').mockImplementation((path) => (path === 'outside.txt' ? Promise.reject(new ApiError({ status: 422, code: 'validation_failed', serverMessage: 'The path leads outside the folder through a symlink' })) : read(path)))
    const { user } = setup('workspace', { source })
    await user.click(await findRow('outside.txt'))
    expect(await screen.findByText('This symlink points outside the folder and can\'t be opened.')).toBeInTheDocument()
    expect(screen.getByText('Symlink to /etc/hosts')).toBeInTheDocument()
    expect(screen.queryByText(/The submitted content didn't pass validation/)).toBeNull()
  })
})
