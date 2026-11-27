import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import { describe, expect, it } from 'vitest'
import { apiUrl } from '@/test/api'
import { server } from '@/test/server'
import type { ReplyFolderContext } from './local-images'
import { Markdown } from './Markdown'

const ROOT = '/var/work/agents/a1/workspace'
const PNG_BYTES = new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a])

function makeFolder(overrides: Partial<ReplyFolderContext> = {}): ReplyFolderContext {
  return {
    roots: [ROOT],
    rootsReady: true,
    contentURL: (rel) => apiUrl(`/agents/a1/folder/content?${new URLSearchParams({ path: rel })}`),
    rawURL: (rel) => apiUrl(`/agents/a1/folder/raw/${rel.split('/').map(encodeURIComponent).join('/')}`),
    folderLink: (rel) => `/p/demo/agents/a1/folder?${new URLSearchParams({ path: rel })}`,
    checkDirectory: async () => 'not-dir',
    maxBytes: 20 << 20,
    ...overrides,
  }
}

function mockRawFiles(
  files: Record<string, number>,
  options: { dirs?: string[]; escaping?: string[]; images?: Record<string, Uint8Array> } = {},
) {
  const dirs = new Set(options.dirs ?? [])
  const escaping = new Set(options.escaping ?? [])
  const headCounts: Record<string, number> = {}

  server.use(
    http.head(apiUrl('/agents/a1/folder/raw/*'), ({ request }) => {
      const encoded = new URL(request.url).pathname.split('/folder/raw/')[1] ?? ''
      const rel = encoded.split('/').map(decodeURIComponent).join('/')
      headCounts[rel] = (headCounts[rel] ?? 0) + 1
      if (dirs.has(rel) || escaping.has(rel)) return new HttpResponse(null, { status: 422 })
      const size = files[rel]
      if (size === undefined) return new HttpResponse(null, { status: 404 })
      return new HttpResponse(null, { status: 200, headers: { 'Content-Length': String(size) } })
    }),
    http.get(apiUrl('/agents/a1/folder/content'), ({ request }) => {
      const rel = new URL(request.url).searchParams.get('path') ?? ''
      const body = options.images?.[rel]
      if (!body) return new HttpResponse(null, { status: 404 })
      return new HttpResponse(body, { headers: { 'Content-Type': 'application/octet-stream' } })
    }),
  )
  return headCounts
}

function LocationEcho() {
  const location = useLocation()
  return <div data-testid="location">{`${location.pathname}${location.search}`}</div>
}

describe('Reply file cards', () => {
  it('renders ChatGPT-style cards after blocks for links, inline code, bare paths, and replaces path-only fenced blocks while keeping link text and deduping', async () => {
    const headCounts = mockRawFiles({
      'pelican-night-ride.html': 18 * 1024,
      'night-market-report.pdf': Math.round(1.2 * 1024 * 1024),
      'README.md': 3 * 1024,
      'night-market-sales-2026-11.csv': 96 * 1024,
      'build-output.zip': Math.round(2.4 * 1024 * 1024),
      'src/app.ts': 4096,
    })
    const folder = makeFolder()
    const reply = [
      `Animation ready: [Open Pelican](${ROOT}/pelican-night-ride.html) and \`night-market-report.pdf\`.`,
      '',
      '```text',
      `${ROOT}/pelican-night-ride.html`,
      '```',
      '',
      'Also wrote ./README.md and exported `night-market-sales-2026-11.csv`.',
      '',
      '```text',
      `${ROOT}/build-output.zip`,
      '```',
      '',
      'Repeated link [Again](README.md) and source code `src/app.ts`.',
    ].join('\n')

    const { container } = render(<Markdown folder={folder} text={reply} />)

    // Inline link keeps its link text and still opens the raw file in a new tab.
    const inlineLink = await screen.findByRole('link', { name: 'Open Pelican' })
    expect(inlineLink).toHaveAttribute('href', folder.rawURL!('pelican-night-ride.html'))
    expect(inlineLink).toHaveAttribute('target', '_blank')

    // All 6 files appear as cards, each exactly once, and both path-only fenced code blocks are gone (no <pre>).
    await waitFor(() => expect(container.querySelectorAll('[data-md-file-card]')).toHaveLength(6))
    expect(container.querySelectorAll('pre')).toHaveLength(0)

    const cards = [...container.querySelectorAll<HTMLElement>('[data-md-file-card]')]
    expect(cards.map((c) => c.getAttribute('data-md-file-card'))).toEqual([
      'pelican-night-ride.html',
      'night-market-report.pdf',
      'README.md',
      'night-market-sales-2026-11.csv',
      'build-output.zip',
      'src/app.ts',
    ])

    // Kinds, colors, names, and sizes match the specification.
    expect(cards[0]).toHaveAttribute('data-md-file-card-kind', 'webpage')
    expect(cards[0]).toHaveTextContent('pelican-night-ride.html')
    expect(cards[0]).toHaveTextContent('Web page · 18 KB')
    expect(cards[0]!.querySelector<HTMLElement>('.md-file-card-icon')).toHaveStyle({ backgroundColor: '#7c3aed' })

    expect(cards[1]).toHaveAttribute('data-md-file-card-kind', 'pdf')
    expect(cards[1]).toHaveTextContent('PDF · 1.2 MB')
    expect(cards[1]!.querySelector<HTMLElement>('.md-file-card-icon')).toHaveStyle({ backgroundColor: '#ef4444' })

    expect(cards[2]).toHaveAttribute('data-md-file-card-kind', 'markdown')
    expect(cards[2]).toHaveTextContent('Markdown · 3 KB')
    expect(cards[2]!.querySelector<HTMLElement>('.md-file-card-icon')).toHaveStyle({ backgroundColor: '#2563eb' })

    expect(cards[3]).toHaveAttribute('data-md-file-card-kind', 'table')
    expect(cards[3]).toHaveTextContent('Spreadsheet · 96 KB')
    expect(cards[3]!.querySelector<HTMLElement>('.md-file-card-icon')).toHaveStyle({ backgroundColor: '#10a37f' })

    expect(cards[4]).toHaveAttribute('data-md-file-card-kind', 'archive')
    expect(cards[4]).toHaveTextContent('Archive · 2.4 MB')
    expect(cards[4]!.querySelector<HTMLElement>('.md-file-card-icon')).toHaveStyle({ backgroundColor: '#64748b' })

    expect(cards[5]).toHaveAttribute('data-md-file-card-kind', 'code')
    expect(cards[5]).toHaveTextContent('app.ts')
    expect(cards[5]).toHaveTextContent('Code · 4 KB')
    expect(cards[5]!.querySelector<HTMLElement>('.md-file-card-icon')).toHaveStyle({ backgroundColor: '#475569' })

    // Primary card link carries an accessible name, full path tooltip, and sandboxed raw URL.
    const primaryLink = within(cards[5]!).getByRole('link', { name: 'app.ts, Code · 4 KB, open in new tab' })
    expect(primaryLink).toHaveAttribute('href', folder.rawURL!('src/app.ts'))
    expect(primaryLink).toHaveAttribute('target', '_blank')
    expect(primaryLink).toHaveAttribute('rel', 'noopener noreferrer')
    expect(primaryLink).toHaveAttribute('title', 'src/app.ts')

    // ProbeStore deduplicated HEAD probes so each path was probed only once.
    expect(headCounts['pelican-night-ride.html']).toBe(1)
    expect(headCounts['README.md']).toBe(1)
  })

  it('shows a gray non-clickable card for deleted files while leaving uncertain non-existent code tokens untouched', async () => {
    mockRawFiles({ 'live.html': 2048 })
    const folder = makeFolder()
    const reply = [
      `Old file: [pelican-bike.html](${ROOT}/pelican-bike.html)`,
      '',
      '```text',
      `${ROOT}/gone-fence.pdf`,
      '```',
      '',
      'Ordinary code token `console.log` and live file `live.html`.',
    ].join('\n')

    const { container } = render(<Markdown folder={folder} text={reply} />)

    await waitFor(() => expect(container.querySelectorAll('[data-md-file-card]')).toHaveLength(3))
    expect(container.querySelectorAll('pre')).toHaveLength(0)

    const missingHtml = container.querySelector<HTMLElement>('[data-md-file-card="pelican-bike.html"]')!
    expect(missingHtml).toHaveAttribute('data-md-file-card-state', 'missing')
    expect(missingHtml).toHaveTextContent('pelican-bike.html')
    expect(missingHtml).toHaveTextContent('No longer in folder')
    expect(within(missingHtml).queryByRole('link')).toBeNull()
    expect(within(missingHtml).queryByRole('button')).toBeNull()

    const missingPdf = container.querySelector<HTMLElement>('[data-md-file-card="gone-fence.pdf"]')!
    expect(missingPdf).toHaveAttribute('data-md-file-card-state', 'missing')
    expect(missingPdf).toHaveTextContent('No longer in folder')

    // Uncertain missing token console.log never becomes a missing card.
    expect(container.querySelector('[data-md-file-card="console.log"]')).toBeNull()
  })

  it('never renders file cards for images, directories, outside paths, escaping symlinks, or HTTP links', async () => {
    mockRawFiles(
      { 'assets/shot.png': PNG_BYTES.byteLength },
      { dirs: ['assets'], escaping: ['escape.html'], images: { 'assets/shot.png': PNG_BYTES } },
    )
    const folder = makeFolder({
      checkDirectory: async (rel) => (rel === 'assets' ? 'dir' : 'not-dir'),
    })
    const reply = [
      `Image: \`${ROOT}/assets/shot.png\` and ![Shot](assets/shot.png).`,
      '',
      `Directory: [Assets](${ROOT}/assets) and [Assets slash](assets/).`,
      '',
      'Outside & HTTP: [Secret](/etc/passwd), [Escape](escape.html), [Site](https://example.com/report.pdf).',
      '',
      '```text',
      `${ROOT}/assets`,
      '```',
    ].join('\n')

    const { container } = render(<Markdown folder={folder} text={reply} />)

    // Wait for directory link and image thumbnail to finish loading.
    expect(await screen.findByRole('link', { name: 'Assets' })).toHaveAttribute('href', folder.folderLink!('assets'))
    expect(await screen.findByRole('img', { name: 'Shot' })).toBeInTheDocument()
    await waitFor(() => expect(screen.getByText('Escape').closest('[title]')).toHaveAttribute('title', 'Outside this folder; cannot be opened'))

    expect(container.querySelectorAll('[data-md-file-card]')).toHaveLength(0)
    // Directory fenced block stays a code block instead of being replaced by a card.
    expect(container.querySelectorAll('pre')).toHaveLength(1)
  })

  it('reveals folder and open-in-new-tab actions on keyboard focus, shows tooltips, and navigates to the Folder tab', async () => {
    mockRawFiles({ 'docs/spec.pdf': 512 * 1024 })
    const folder = makeFolder()
    const user = userEvent.setup()

    const { container } = render(
      <MemoryRouter initialEntries={['/p/demo/agents/a1/chat']}>
        <Routes>
          <Route path="/p/demo/agents/a1/chat" element={<Markdown folder={folder} text={`Read [Spec](${ROOT}/docs/spec.pdf).`} />} />
          <Route path="/p/demo/agents/a1/folder" element={<LocationEcho />} />
        </Routes>
      </MemoryRouter>,
    )

    const card = await waitFor(() => {
      const el = container.querySelector<HTMLElement>('[data-md-file-card="docs/spec.pdf"]')
      expect(el).not.toBeNull()
      return el!
    })
    const actionsWrap = card.querySelector<HTMLElement>('[data-md-file-card-actions]')!
    expect(actionsWrap).toHaveAttribute('data-md-file-card-actions', 'hover')

    // Focusing the card via keyboard Tab reveals the two action controls and keeps them reachable by Tab.
    const mainLink = within(card).getByRole('link', { name: 'spec.pdf, PDF · 512 KB, open in new tab' })
    await user.tab()
    // First Tab lands on the inline Markdown link "Spec"; second Tab lands on the card itself.
    expect(document.activeElement).toBe(screen.getByRole('link', { name: 'Spec' }))
    await user.tab()
    expect(document.activeElement).toBe(mainLink)
    expect(card).toHaveAttribute('data-focused', 'true')
    expect(actionsWrap).toHaveAttribute('data-md-file-card-actions', 'visible')

    const folderBtn = within(card).getByRole('link', { name: 'Show in folder' })
    const openBtn = within(card).getByRole('link', { name: 'Open in new tab' })
    expect(folderBtn).toHaveAttribute('href', folder.folderLink!('docs/spec.pdf'))
    expect(openBtn).toHaveAttribute('href', folder.rawURL!('docs/spec.pdf'))
    expect(openBtn).toHaveAttribute('target', '_blank')
    expect(openBtn).toHaveAttribute('rel', 'noopener noreferrer')

    // Tabbing moves directly into the two action buttons while keeping actions visible.
    await user.tab()
    expect(document.activeElement).toBe(folderBtn)
    expect(card).toHaveAttribute('data-focused', 'true')
    await user.tab()
    expect(document.activeElement).toBe(openBtn)
    expect(card).toHaveAttribute('data-focused', 'true')

    // Hovering an action displays its tooltip.
    await user.hover(folderBtn)
    expect(await screen.findByRole('tooltip')).toHaveTextContent('Show in folder')

    // Clicking "Show in folder" navigates via React Router to the Folder tab with the file selected.
    await user.click(folderBtn)
    expect(await screen.findByTestId('location')).toHaveTextContent('/p/demo/agents/a1/folder?path=docs%2Fspec.pdf')
  })

  it('omits the Show in folder action when folderLink is unavailable (task node conversations)', async () => {
    mockRawFiles({ 'report.html': 4096 })
    const folder = makeFolder({ folderLink: undefined, checkDirectory: undefined })
    const { container } = render(<Markdown folder={folder} text={`Saved to [Report](${ROOT}/report.html).`} />)

    const card = await waitFor(() => {
      const el = container.querySelector<HTMLElement>('[data-md-file-card="report.html"]')
      expect(el).not.toBeNull()
      return el!
    })
    expect(within(card).queryByRole('link', { name: 'Show in folder' })).toBeNull()
    expect(within(card).getByRole('link', { name: 'Open in new tab' })).toHaveAttribute('href', folder.rawURL!('report.html'))
  })

  it('collapses more than 6 cards in one place and expands when clicking the remaining-count button', async () => {
    const files: Record<string, number> = {}
    const links: string[] = []
    for (let i = 1; i <= 8; i++) {
      const name = `file-${i}.md`
      files[name] = i * 1024
      links.push(`[File ${i}](${name})`)
    }
    mockRawFiles(files)
    const folder = makeFolder()
    const user = userEvent.setup()

    const { container } = render(<Markdown folder={folder} text={`Created: ${links.join(', ')}.`} />)

    await waitFor(() => expect(container.querySelectorAll('[data-md-file-card]')).toHaveLength(6))
    const moreBtn = screen.getByRole('button', { name: '2 more files' })
    expect(moreBtn).toHaveAttribute('aria-expanded', 'false')

    await user.click(moreBtn)
    expect(container.querySelectorAll('[data-md-file-card]')).toHaveLength(8)
    expect(screen.queryByRole('button', { name: '2 more files' })).toBeNull()
  })

  it('keeps the streaming cursor inline and defers file cards until streaming finishes', async () => {
    mockRawFiles({ 'pelican.html': 18 * 1024 })
    const folder = makeFolder()
    const { container, rerender } = render(<Markdown folder={folder} cursor text={`Working on [Pelican](${ROOT}/pelican.html)`} />)

    const cursor = container.querySelector('.riso-cursor')!
    expect(cursor.parentElement?.tagName).toBe('P')
    expect(container.querySelectorAll('[data-md-file-card]')).toHaveLength(0)

    rerender(<Markdown folder={folder} cursor={false} text={`Working on [Pelican](${ROOT}/pelican.html)`} />)
    await waitFor(() => expect(container.querySelectorAll('[data-md-file-card]')).toHaveLength(1))
  })
})
