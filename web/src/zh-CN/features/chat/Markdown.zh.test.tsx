import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl } from '@/test/api'
import { server } from '@/test/server'
import { Markdown } from '@/features/chat/Markdown'
import type { ReplyFolderContext } from '@/features/chat/local-images'

// Footnotes use the UI copy instead of remark's built-in English labels. In English the copy reads the same as the built-in labels,
// so this is checked in Chinese. Moved from src/features/chat/Markdown.test.tsx, which keeps the same scenario in English.

describe('Markdown rendering and safety', () => {
  it('footnotes use the Chinese copy, jump to the note of this reply on the current page, and ids do not repeat across replies', () => {
    const text = '文字[^1]\n\n[^1]: 注释'
    const { container } = render(
      <>
        <div data-testid="first"><Markdown text={text} /></div>
        <div data-testid="second"><Markdown text={text} /></div>
      </>,
    )
    const ids = [...container.querySelectorAll('[id]')].map((el) => el.id)
    expect(ids.length).toBeGreaterThan(0)
    expect(new Set(ids).size).toBe(ids.length)
    expect(container).not.toHaveTextContent('Footnotes')
    expect(screen.getAllByText('脚注')).toHaveLength(2)

    for (const testId of ['first', 'second']) {
      const reply = screen.getByTestId(testId)
      const ref = within(reply).getByRole('link', { name: '1' })
      expect(ref).not.toHaveAttribute('target')
      const note = container.querySelector(`[id="${ref.getAttribute('href')!.slice(1)}"]`)!
      expect(reply).toContainElement(note as HTMLElement)
      expect(note).toHaveTextContent('注释')
      const label = container.querySelector(`[id="${ref.getAttribute('aria-describedby')}"]`)
      expect(reply).toContainElement(label as HTMLElement)
      const back = within(reply).getByRole('link', { name: '返回引用 1' })
      expect(back).not.toHaveAttribute('target')
      expect(container.querySelector(`[id="${back.getAttribute('href')!.slice(1)}"]`)).toBe(ref)
    }
  })

  it('reply image preview and fallback copy use Chinese UI text', async () => {
    const user = userEvent.setup()
    const { rerender } = render(<Markdown text="![候选图](https://example.com/a.png)" />)
    await user.click(screen.getByRole('button', { name: '放大图片：候选图' }))
    const dialog = await screen.findByRole('dialog', { name: '候选图' })
    expect(within(dialog).getByRole('link', { name: '在新标签页打开原图' })).toHaveAttribute('href', 'https://example.com/a.png')
    await user.keyboard('{Escape}')

    rerender(<Markdown text="![](https://example.com/broken.png)" />)
    fireEvent.error(document.querySelector('img')!)
    expect(screen.getByRole('img', { name: '图片加载失败' })).toHaveTextContent('图片加载失败')
  })

  it('keeps Chinese caption lines grouped with their images in one row', () => {
    const reply = [
      '做了四张候选图：',
      '',
      '2D-1：红蓝撞色，适合做封面',
      '',
      '![2D-1](https://example.com/1.png)',
      '',
      '2D-2：黄底黑线，风格更轻松',
      '',
      '![2D-2](https://example.com/2.png)',
      '',
      '2D-3：极简留白，突出主体',
      '',
      '![2D-3](https://example.com/3.png)',
      '',
      '2D-4：复古印刷质感',
      '',
      '![2D-4](https://example.com/4.png)',
    ].join('\n')
    const { container } = render(<Markdown text={reply} />)
    expect([...container.querySelectorAll('p')].map((p) => p.textContent)).toEqual(['做了四张候选图：'])
    const row = container.querySelector('[data-md-image-row]')!
    expect(row).toHaveAttribute('data-count', '4')
    const figures = [...row.querySelectorAll<HTMLElement>('figure.md-image-item')]
    expect(figures).toHaveLength(4)
    expect(figures[0].firstElementChild).toHaveTextContent('2D-1：红蓝撞色，适合做封面')
    expect(within(figures[3]).getByRole('img', { name: '2D-4' })).toBeInTheDocument()
  })

  it('groups two-line Chinese captions with hard breaks and local file links into rows split by headings and dividers', () => {
    const reply = [
      '4 张 **Loop Now** 卡通形象候选图都已经用 `nano-banana-pro`（1:1，2K）生成完成，实际消耗 **60 Tapies**（当前剩余 **1,803 Tapies**），原图全部存进我自己的工作目录并做了本地 Git 提交（`8fffd3f`）。',
      '',
      '### 2D 粗边印刷/贴纸风（对齐现有封面风格）',
      '',
      '**2D-1**：笑眼很萌，但右上角开口处多了一根细白连线。  ',
      '本地文件：[mascot-2d-1.png](file:///workspace/assets/mascot/mascot-2d-1.png)',
      '![Loop Now 2D-1](https://example.com/2d-1.png)',
      '',
      '**2D-2（2D 首推）**：右上角小黄块干净悬浮，身体开口和 Logo 一致，配色和现有 Riso 封面最搭。  ',
      '本地文件：[mascot-2d-2.png](file:///workspace/assets/mascot/mascot-2d-2.png)',
      '![Loop Now 2D-2](https://example.com/2d-2.png)',
      '',
      '---',
      '',
      '### 3D 潮玩/盲盒风',
      '',
      '**3D-1**：盲盒材质很好，不过右上角开口切得偏小，Logo 轮廓不如第二张明显。  ',
      '本地文件：[mascot-3d-1.png](file:///workspace/assets/mascot/mascot-3d-1.png)',
      '![Loop Now 3D-1](https://example.com/3d-1.png)',
      '',
      '**3D-2（3D 首推）**：右上角开口清楚，一眼能认出是 Loop Now Logo 变的，表情和悬浮小金块都很讨喜。  ',
      '本地文件：[mascot-3d-2.png](file:///workspace/assets/mascot/mascot-3d-2.png)',
      '![Loop Now 3D-2](https://example.com/3d-2.png)',
    ].join('\n')
    const { container } = render(<Markdown text={reply} />)
    const rows = [...container.querySelectorAll<HTMLElement>('[data-md-image-row]')]
    expect(rows).toHaveLength(2)
    expect(rows.map((row) => row.getAttribute('data-count'))).toEqual(['2', '2'])
    expect([...container.querySelectorAll('h3')].map((h) => h.textContent)).toEqual([
      '2D 粗边印刷/贴纸风（对齐现有封面风格）',
      '3D 潮玩/盲盒风',
    ])
    expect(container.querySelectorAll('hr')).toHaveLength(1)
    expect(within(rows[0]).getAllByRole('img').map((img) => img.getAttribute('alt'))).toEqual(['Loop Now 2D-1', 'Loop Now 2D-2'])
    expect(within(rows[1]).getAllByRole('img').map((img) => img.getAttribute('alt'))).toEqual(['Loop Now 3D-1', 'Loop Now 3D-2'])
  })

  it('renders local file links and inline-code image paths with Chinese tooltips and lightbox labels', async () => {
    const root = '/Users/Shared/loop-now-desktop/default/agents/01a217ac/workspace'
    const png = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])
    const svg = '<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40"><circle cx="20" cy="20" r="16"/></svg>'
    server.use(
      http.get(apiUrl('/agents/a1/folder/content'), ({ request }) => {
        const rel = new URL(request.url).searchParams.get('path') ?? ''
        if (rel.endsWith('.png')) return new HttpResponse(png, { headers: { 'Content-Type': 'application/octet-stream' } })
        if (rel.endsWith('.svg')) return new HttpResponse(svg, { headers: { 'Content-Type': 'application/octet-stream' } })
        return new HttpResponse(null, { status: 404 })
      }),
      http.head(apiUrl('/agents/a1/folder/raw/*'), ({ request }) => {
        const rel = new URL(request.url).pathname.split('/folder/raw/')[1] ?? ''
        if (rel.endsWith('.png') || rel.endsWith('.svg') || rel === 'pelican.html') return new HttpResponse(null, { status: 200 })
        if (rel === 'assets') return new HttpResponse(null, { status: 422 })
        return new HttpResponse(null, { status: 404 })
      }),
    )
    const folder: ReplyFolderContext = {
      roots: [root],
      contentURL: (rel) => apiUrl(`/agents/a1/folder/content?${new URLSearchParams({ path: rel })}`),
      rawURL: (rel) => apiUrl(`/agents/a1/folder/raw/${rel.split('/').map(encodeURIComponent).join('/')}`),
      folderLink: (rel) => `/p/loop-now/agents/a1/folder?${new URLSearchParams({ path: rel })}`,
      checkDirectory: async (rel) => (rel === 'assets' ? 'dir' : 'not-dir'),
      maxBytes: 20 << 20,
    }
    const reply = [
      `2×2 对比预览图 preview-2x2.png：\`${root}/assets/mascot/svg-concepts/preview-2x2.png\``,
      '',
      `- **方案 1**：\`${root}/assets/mascot/svg-concepts/concept-1.svg\``,
      `- **方案 2**：\`${root}/assets/mascot/svg-concepts/concept-2.svg\``,
      `- **方案 3**：\`${root}/assets/mascot/svg-concepts/concept-3.svg\``,
      `- **方案 4**：\`${root}/assets/mascot/svg-concepts/concept-4.svg\``,
      '',
      `[打开动画](${root}/pelican.html) · [资源目录](assets/) · [目录外](/etc/passwd) · [已删除](gone.html)`,
    ].join('\n')

    const user = userEvent.setup()
    const { container } = render(<Markdown folder={folder} text={reply} />)

    const link = await screen.findByRole('link', { name: `${root}/assets/mascot/svg-concepts/preview-2x2.png` })
    expect(link).toHaveAttribute('title', '在新标签页打开')
    expect(link).toHaveAttribute('target', '_blank')
    expect(await screen.findByRole('link', { name: '打开动画' })).toHaveAttribute('title', '在新标签页打开')
    expect(await screen.findByRole('link', { name: '资源目录' })).toHaveAttribute('title', '在文件夹中打开')
    expect(screen.queryByRole('link', { name: '目录外' })).toBeNull()
    expect(screen.getByText('目录外').closest('[title]')).toHaveAttribute('title', '路径不在文件夹内，不能打开')
    await waitFor(() => expect(screen.getByText('已删除').closest('[title]')).toHaveAttribute('title', '文件夹里找不到这个文件，不能打开'))
    expect(screen.queryByRole('link', { name: '已删除' })).toBeNull()

    expect(await screen.findByRole('button', { name: '放大图片：preview-2x2.png' })).toBeInTheDocument()
    await waitFor(() => expect(container.querySelector('[data-md-local-images] [data-md-image-row]')).toHaveAttribute('data-count', '4'))

    await user.click(screen.getByRole('button', { name: '放大图片：concept-1.svg' }))
    const dialog = await screen.findByRole('dialog', { name: 'concept-1.svg' })
    expect(within(dialog).getByRole('link', { name: '在新标签页打开原图' })).toHaveAttribute('href', folder.rawURL!('assets/mascot/svg-concepts/concept-1.svg'))
  })

  it('renders file cards with Chinese kind labels, missing-file text, tooltips, and collapse button', async () => {
    const root = '/Users/Shared/loop-now-desktop/default/agents/01a217ac/workspace'
    const sizes: Record<string, number> = {
      'pelican-night-ride.html': 18 * 1024,
      'night-market-report.pdf': Math.round(1.2 * 1024 * 1024),
      'README.md': 3 * 1024,
      'night-market-sales-2026-11.csv': 96 * 1024,
      'build-output.zip': Math.round(2.4 * 1024 * 1024),
      'extra-1.txt': 1024,
      'extra-2.txt': 2048,
      'extra-3.txt': 3072,
    }
    server.use(
      http.head(apiUrl('/agents/a1/folder/raw/*'), ({ request }) => {
        const rel = new URL(request.url).pathname.split('/folder/raw/')[1] ?? ''
        const size = sizes[rel]
        if (size === undefined) return new HttpResponse(null, { status: 404 })
        return new HttpResponse(null, { status: 200, headers: { 'Content-Length': String(size) } })
      }),
    )
    const folder: ReplyFolderContext = {
      roots: [root],
      rootsReady: true,
      contentURL: (rel) => apiUrl(`/agents/a1/folder/content?${new URLSearchParams({ path: rel })}`),
      rawURL: (rel) => apiUrl(`/agents/a1/folder/raw/${rel.split('/').map(encodeURIComponent).join('/')}`),
      folderLink: (rel) => `/p/loop-now/agents/a1/folder?${new URLSearchParams({ path: rel })}`,
      checkDirectory: async () => 'not-dir',
      maxBytes: 20 << 20,
    }
    const reply = [
      `夜市画面已经画好：[打开动画](${root}/pelican-night-ride.html)，报告见 \`night-market-report.pdf\`。`,
      '',
      '```text',
      `${root}/pelican-night-ride.html`,
      '```',
      '',
      `已删除：[pelican-bike.html](${root}/pelican-bike.html)`,
      '',
      '全部附件：[README](README.md) [表格](night-market-sales-2026-11.csv) [压缩包](build-output.zip) [一](extra-1.txt) [二](extra-2.txt) [三](extra-3.txt) [四](missing-7.md)',
    ].join('\n')

    const user = userEvent.setup()
    const { container } = render(<Markdown folder={folder} text={reply} />)

    await waitFor(() => expect(container.querySelector('[data-md-file-card="pelican-night-ride.html"]')).toHaveTextContent('网页 · 18 KB'))
    expect(container.querySelector('pre')).toBeNull()
    expect(container.querySelector('[data-md-file-card="night-market-report.pdf"]')).toHaveTextContent('PDF · 1.2 MB')
    const missingCard = container.querySelector<HTMLElement>('[data-md-file-card="pelican-bike.html"]')!
    expect(missingCard).toHaveAttribute('data-md-file-card-state', 'missing')
    expect(missingCard).toHaveTextContent('已不在文件夹里')

    const pelicanCard = container.querySelector<HTMLElement>('[data-md-file-card="pelican-night-ride.html"]')!
    expect(within(pelicanCard).getByRole('link', { name: '在文件夹中显示' })).toHaveAttribute('href', folder.folderLink!('pelican-night-ride.html'))
    expect(within(pelicanCard).getByRole('link', { name: '新标签页打开' })).toHaveAttribute('href', folder.rawURL!('pelican-night-ride.html'))

    // Third block has 7 cards -> shows 6 plus "还有 1 个文件".
    await waitFor(() => expect(container.querySelector('[data-md-file-card="README.md"]')).toHaveTextContent('Markdown · 3 KB'))
    expect(container.querySelector('[data-md-file-card="night-market-sales-2026-11.csv"]')).toHaveTextContent('表格 · 96 KB')
    expect(container.querySelector('[data-md-file-card="build-output.zip"]')).toHaveTextContent('压缩包 · 2.4 MB')
    const more = screen.getByRole('button', { name: '还有 1 个文件' })
    await user.click(more)
    expect(container.querySelector('[data-md-file-card="missing-7.md"]')).toHaveTextContent('已不在文件夹里')
  })
})
