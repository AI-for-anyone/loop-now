import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { renderWithQuery } from '@/test/render'
import { createMockFolder } from '@/test/folders'
import { t } from '@/i18n'
import { FileManager } from './FileManager'

// 改版“一张印出来的纸”第 2 步：Agent 的文件夹页签与项目文件夹页放在纸张上（variant="sheet"）。
const limits = { maxBytes: 1 << 20, extensions: ['.md', '.go', '.ts', '.json'] }

function setup(preset: 'workspace' | 'project' = 'workspace') {
  const source = createMockFolder({ key: preset, preset, latency: 0, git: preset === 'workspace', gitDuration: 400, uploadDuration: 0 })
  const user = userEvent.setup()
  const view = renderWithQuery(<FileManager variant="sheet" source={source} rootLabel="server 的文件夹" limits={limits} className="h-[600px]" />)
  return { source, user, ...view }
}

const content = () => screen.getByRole('region', { name: '文件' })

describe('纸张上的文件夹', () => {
  it('直接排在纸上，不再套一层卡片；工具条在顶部：新建、上传、从 Git 拉取都是带字的藏青描边胶囊', async () => {
    const { container } = setup()
    const manager = container.querySelector('[data-file-manager]')!
    expect(manager).not.toHaveClass('rounded-card')
    expect(manager).not.toHaveClass('bg-white')
    for (const name of ['新建文件', '新建文件夹', '上传', '从 Git 拉取']) {
      const button = await screen.findByRole('button', { name })
      expect(button).toHaveClass('h-[34px]', 'rounded-full', 'border-2', 'border-ink', 'bg-paper-raised')
      expect(button).toHaveTextContent(name)
    }
    // 根目录名称前是一个黄色的文件夹块。
    expect(screen.getByRole('navigation', { name: '位置' }).querySelector('[data-folder-icon]')).toHaveClass('bg-riso-yellow', 'border-2', 'border-ink')
  })

  it('还没有 AGENTS.md：粉色投影的卡片', async () => {
    setup('project')
    const note = await screen.findByRole('note')
    expect(note).toHaveTextContent('还没有 AGENTS.md')
    expect(note).toHaveClass('bg-tint-pink-2', 'border-2', 'border-ink', 'shadow-print-pink')
  })

  it('文件列表：表头下一道实线，行与行之间虚线；文件夹是黄色的文件夹块，文件是白色的纸；大小与时间用等宽字', async () => {
    setup()
    const table = await within(content()).findByRole('table')
    expect(table.querySelector('thead tr')).toHaveClass('border-b-2', 'border-ink')
    const rows = [...table.querySelectorAll('tbody tr')]
    expect(rows.length).toBeGreaterThan(2)
    for (const row of rows) expect(row).toHaveClass('border-b-[1.5px]', 'border-dashed')
    const dir = rows.find((row) => row.querySelector('[data-folder-icon]'))!
    const file = rows.find((row) => row.querySelector('[data-file-icon]'))!
    expect(dir.querySelector('[data-folder-icon]')).toHaveClass('bg-riso-yellow')
    expect(file.querySelector('[data-file-icon]')).toHaveClass('bg-white', 'border-2', 'border-ink')
    const cells = file.querySelectorAll('td')
    expect(cells[1]).toHaveClass('font-mono')
    expect(cells[2]).toHaveClass('font-mono')
  })

  it('文件树里选中的一项是黄色', async () => {
    const { user } = setup()
    const tree = await screen.findByRole('tree')
    // 一开始选中的是根目录。
    const root = within(tree).getAllByRole('treeitem')[0]
    expect(root).toHaveAttribute('aria-selected', 'true')
    expect(root).toHaveClass('bg-riso-yellow')
    // 打开另一个文件夹后，黄色跟着走。
    const web = () => tree.querySelector<HTMLElement>('[role=treeitem][data-path="web"]')
    await waitFor(() => expect(web()).not.toBeNull())
    await user.click(web()!)
    await waitFor(() => expect(web()).toHaveAttribute('aria-selected', 'true'))
    expect(web()).toHaveClass('bg-riso-yellow')
    expect(root).not.toHaveClass('bg-riso-yellow')
  })

  it('底部一道虚线，下面是说明', async () => {
    const { container } = setup()
    await within(content()).findByRole('table')
    const footer = container.querySelector('[data-folder-footer]')!
    expect(footer).toHaveClass('border-t-2', 'border-dashed')
    expect(footer).toHaveTextContent('Agent 可能正在同时修改这些文件；同一个文件以最后一次保存为准。')
  })

  it('票根“概况”写着当前文件夹里的文件夹数、文件数与文件总大小；窄屏不显示', async () => {
    const { source } = setup()
    const { entries } = await source.list('', null)
    const dirs = entries.filter((entry) => entry.type === 'dir').length
    const files = entries.filter((entry) => entry.type === 'file')
    const bytes = files.reduce((sum, entry) => sum + (entry.size ?? 0), 0)
    const stub = await screen.findByRole('complementary', { name: '概况' })
    expect(stub).toHaveClass('max-nav:hidden', 'border-dashed')
    await waitFor(() => expect(within(stub).getByText('文件夹')).toBeInTheDocument())
    await waitFor(() => expect(within(stub).getByText('文件夹').previousElementSibling).toHaveTextContent(String(dirs)))
    expect(within(stub).getByText('文件').previousElementSibling).toHaveTextContent(String(files.length))
    const [value, unit] = t.folders.size(bytes).split(' ')
    expect(within(stub).getByText(unit).previousElementSibling).toHaveTextContent(value)
  })

  it('卡片版（设置页里的共享文件夹）照旧：有边框、没有票根', async () => {
    const source = createMockFolder({ key: 'card', preset: 'project', latency: 0 })
    const { container } = renderWithQuery(<FileManager source={source} rootLabel="项目文件夹" limits={limits} className="h-[600px]" />)
    await screen.findByRole('note')
    expect(container.querySelector('[data-file-manager]')).toHaveClass('rounded-card')
    expect(screen.queryByRole('complementary', { name: '概况' })).toBeNull()
  })
})
