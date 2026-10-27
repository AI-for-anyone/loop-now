import { screen, waitFor, within } from '@testing-library/react'
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
  const view = renderWithQuery(<FileManager source={source} rootLabel="server 的文件夹" limits={limits} maxUpload={options.maxUpload} className="h-[600px]" />)
  return { source, user, ...view }
}
const tree = () => screen.getByRole('tree')
const row = (path: string) => tree().querySelector<HTMLElement>(`[data-path="${path}"]`)
const findRow = (path: string) => waitFor(() => { const el = row(path); if (!el) throw new Error(`no row ${path}`); return el })
const menu = async (user: ReturnType<typeof userEvent.setup>, path: string, item: string) => {
  await user.click(within(await findRow(path)).getByRole('button', { name: /更多操作/ }))
  await user.click(await screen.findByRole('menuitem', { name: item }))
}

describe('文件管理器', () => {
  it('只读取展开了的文件夹；根目录的 AGENTS.md 排在最前、标明入口，并像 README 一样显示内容；大文件夹分页读取', async () => {
    const { source, user } = setup()
    const list = vi.spyOn(source, 'list')
    const entry = await findRow('AGENTS.md')
    expect(tree().querySelectorAll('[role=treeitem]')[1]).toBe(entry)
    expect(within(entry).getByText('入口')).toBeInTheDocument()
    expect(await within(screen.getByRole('region', { name: 'AGENTS.md' })).findByText('你负责 Loop Now 服务端（Go）的开发与维护。')).toBeInTheDocument()
    expect(list.mock.calls.map(([path]) => path)).toEqual([])

    await user.click(await findRow('web'))
    await user.click(await findRow('web/node_modules'))
    await findRow('web/node_modules/@babel')
    expect(list.mock.calls.map(([path, cursor]) => [path, cursor])).toEqual([['web', null], ['web/node_modules', null]])
    const deep = () => tree().querySelectorAll('[role=treeitem][aria-level="4"][data-path]').length
    expect(deep()).toBe(100)
    await user.click(within(tree()).getByText('显示更多'))
    await waitFor(() => expect(deep()).toBe(186))
    expect(within(tree()).queryByText('显示更多')).toBeNull()
  })

  it('打开文件查看，就地编辑：有未保存的修改时离开要确认；保存照原样写回 BOM 与 CRLF', async () => {
    const source = createMockFolder({ key: 'w', preset: 'workspace', latency: 0 })
    await source.write('crlf.md', '﻿# 记录\r\n- 一\r\n', true)
    const write = vi.spyOn(source, 'write')
    const { user } = setup('workspace', { source })
    await user.click(await findRow('crlf.md'))
    await user.click(await screen.findByRole('button', { name: '编辑' }))
    const editor = await screen.findByRole('textbox', { name: 'crlf.md' })
    expect(editor).toHaveValue('# 记录\n- 一\n')
    await user.type(editor, '- 二')
    expect(screen.getByText('未保存')).toBeInTheDocument()

    await user.click(await findRow('go.mod'))
    const confirm = await screen.findByRole('alertdialog', { name: '放弃未保存的修改？' })
    await user.click(within(confirm).getByRole('button', { name: '取消' }))
    expect(editor).toHaveValue('# 记录\n- 一\n- 二')

    await user.click(editor)
    await user.keyboard('{Meta>}s{/Meta}')
    await waitFor(() => expect(write).toHaveBeenCalledWith('crlf.md', '﻿# 记录\r\n- 一\r\n- 二', false))
    await waitFor(() => expect(screen.queryByText('未保存')).toBeNull())
    expect(screen.queryByRole('textbox', { name: 'crlf.md' })).toBeNull()
  })

  it('就地新建与重命名，删除前确认，移动到其他文件夹', async () => {
    const { source, user } = setup()
    const write = vi.spyOn(source, 'write'), move = vi.spyOn(source, 'move'), remove = vi.spyOn(source, 'remove')
    await findRow('AGENTS.md')

    await user.click(screen.getByRole('button', { name: '新建文件' }))
    await user.type(within(tree()).getByRole('textbox', { name: '文件名' }), 'plan.md{Enter}')
    await waitFor(() => expect(write).toHaveBeenCalledWith('plan.md', '', true))
    expect(await screen.findByRole('textbox', { name: 'plan.md' })).toHaveValue('')
    await user.click(screen.getByRole('button', { name: '取消' }))

    await menu(user, 'README.md', '重命名')
    const name = within(tree()).getByRole('textbox', { name: '文件名' })
    expect(name).toHaveValue('README.md')
    await user.clear(name)
    await user.type(name, 'OVERVIEW.md{Enter}')
    await waitFor(() => expect(move).toHaveBeenCalledWith('README.md', 'OVERVIEW.md'))
    await findRow('OVERVIEW.md')

    await menu(user, 'docs', '删除')
    const confirm = await screen.findByRole('alertdialog', { name: '删除“docs”？' })
    expect(confirm).toHaveTextContent('文件夹里的全部内容会一起删除，不能恢复。')
    await user.click(within(confirm).getByRole('button', { name: '删除' }))
    await waitFor(() => expect(remove).toHaveBeenCalledWith('docs'))
    await waitFor(() => expect(row('docs')).toBeNull())

    await menu(user, 'Makefile', '移动到…')
    const dialog = await screen.findByRole('dialog', { name: '移动“Makefile”' })
    await user.type(within(dialog).getByLabelText('移动到文件夹'), 'scripts')
    await user.click(within(dialog).getByRole('button', { name: '移动' }))
    await waitFor(() => expect(move).toHaveBeenCalledWith('Makefile', 'scripts/Makefile'))
    await waitFor(() => expect(row('Makefile')).toBeNull())
  })

  it('上传到选中的文件夹；超过单个文件上限的不发出，直接标为失败', async () => {
    const { source, user, container } = setup('workspace', { maxUpload: 100 })
    const upload = vi.spyOn(source, 'upload')
    await user.click(await findRow('handoff'))
    await user.click(screen.getByRole('button', { name: '上传' }))
    await user.click(await screen.findByRole('menuitem', { name: '上传文件' }))
    await user.upload(container.querySelector<HTMLInputElement>('input[type=file]:not([webkitdirectory])')!, [new File(['# 测试结果\n'], 'report.md', { type: 'text/markdown' }), new File([new Uint8Array(500)], 'big.bin')])
    const panel = await screen.findByRole('region', { name: '上传' })
    await waitFor(() => expect(within(panel).getByRole('status')).toHaveTextContent('已上传 1 个文件'))
    expect(within(panel).getByText('1 个失败')).toBeInTheDocument()
    expect(within(panel).getByTitle('超过单个文件上限（100 B）')).toHaveTextContent('handoff/big.bin')
    expect(upload).toHaveBeenCalledTimes(1)
    expect(upload.mock.calls[0].slice(0, 2)).toEqual(['handoff', expect.objectContaining({ path: 'report.md' })])
    await findRow('handoff/report.md')
  })

  it('根目录没有 AGENTS.md 时说明它的作用，一键创建后直接编辑；共享文件夹没有“从 Git 拉取”', async () => {
    const { source, user } = setup('project')
    const write = vi.spyOn(source, 'write')
    const note = await screen.findByRole('note')
    expect(note).toHaveTextContent('还没有 AGENTS.md')
    expect(screen.queryByRole('button', { name: '从 Git 拉取' })).toBeNull()
    await user.click(within(note).getByRole('button', { name: '创建 AGENTS.md' }))
    await waitFor(() => expect(write).toHaveBeenCalledWith('AGENTS.md', expect.stringContaining('# 工作说明'), true))
    expect((await screen.findByRole<HTMLTextAreaElement>('textbox', { name: 'AGENTS.md' })).value).toContain('Agent 每次启动时先读这个文件')
  })

  it('从 Git 拉取：进行中与成功显示在状态栏；失败的拉取刷新后仍在，可以看 git 输出', async () => {
    const { source, user, unmount } = setup()
    const start = vi.spyOn(source.git!, 'start')
    await findRow('AGENTS.md')
    await user.click(screen.getByRole('button', { name: '从 Git 拉取' }))
    let dialog = await screen.findByRole('dialog', { name: '从 Git 拉取' })
    await user.type(within(dialog).getByLabelText('仓库地址'), 'https://github.com/acme/budget-ledger.git')
    expect(within(dialog).getByLabelText('放到')).toHaveValue('budget-ledger')
    await user.click(within(dialog).getByRole('button', { name: '开始拉取' }))
    expect(start).toHaveBeenCalledWith({ url: 'https://github.com/acme/budget-ledger.git', branch: 'main', path: 'budget-ledger' })
    expect(await screen.findByText('正在拉取 https://github.com/acme/budget-ledger.git → budget-ledger')).toBeInTheDocument()
    expect(await screen.findByText('已拉取到 budget-ledger', undefined, { timeout: 4000 })).toBeInTheDocument()
    await findRow('budget-ledger')

    await user.click(screen.getByRole('button', { name: '从 Git 拉取' }))
    dialog = await screen.findByRole('dialog', { name: '从 Git 拉取' })
    await user.type(within(dialog).getByLabelText('仓库地址'), 'https://github.com/acme/private-fail.git')
    await user.click(within(dialog).getByRole('button', { name: '开始拉取' }))
    expect(await screen.findByText('拉取失败：无法克隆仓库', undefined, { timeout: 4000 })).toBeInTheDocument()

    // 刷新页面：最近一次失败的拉取仍显示，可以看 git 输出。
    unmount()
    renderWithQuery(<FileManager source={source} rootLabel="server 的文件夹" limits={limits} />, createTestQueryClient())
    expect(await screen.findByText('拉取失败：无法克隆仓库')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '查看 git 输出' }))
    expect(screen.getByText(/could not read Username/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '关闭' }))
    expect(screen.queryByText('拉取失败：无法克隆仓库')).toBeNull()
  })

  it('二进制与超过上限的文件只能下载；软链接显示指向并打开目标的内容', async () => {
    const { user } = setup()
    await user.click(await findRow('data'))
    await user.click(await findRow('data/fixtures.bin'))
    expect(await screen.findByText('二进制文件，不能预览。')).toBeInTheDocument()
    await user.click(await findRow('logs'))
    await user.click(await findRow('logs/server.log'))
    expect(await screen.findByText('文件超过 1.0 MiB，不能预览或在线编辑。')).toBeInTheDocument()
    await user.click(await findRow('current-notes.md'))
    expect(await screen.findByText('软链接，指向 handoff/notes.md')).toBeInTheDocument()
    expect(await screen.findByText('budget 账本的写入顺序已改完，测试待跑。')).toBeInTheDocument()
  })

  it('指向文件夹之外的软链接：服务端拒绝读取时说明原因，不显示通用的校验错误', async () => {
    const source = createMockFolder({ key: 'w', preset: 'workspace', latency: 0 })
    const list = source.list.bind(source), read = source.read.bind(source)
    const outside = { name: 'outside.txt', path: 'outside.txt', type: 'symlink' as const, size: null, modifiedAt: '2026-10-25T08:00:00Z', symlinkTarget: '/etc/hosts' }
    vi.spyOn(source, 'list').mockImplementation(async (path, cursor) => { const page = await list(path, cursor); return path === '' ? { ...page, entries: [...page.entries, outside] } : page })
    vi.spyOn(source, 'read').mockImplementation((path) => (path === 'outside.txt' ? Promise.reject(new ApiError({ status: 422, code: 'validation_failed', serverMessage: '路径经由软链接指向了文件夹之外' })) : read(path)))
    const { user } = setup('workspace', { source })
    await user.click(await findRow('outside.txt'))
    expect(await screen.findByText('这个软链接指向文件夹之外，不能打开。')).toBeInTheDocument()
    expect(screen.getByText('软链接，指向 /etc/hosts')).toBeInTheDocument()
    expect(screen.queryByText(/提交的内容未通过校验/)).toBeNull()
  })
})
