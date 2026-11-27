import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl } from '@/test/api'
import { server } from '@/test/server'
import {
  classifyInlineCodePath,
  classifyMarkdownLinkHref,
  probeLocalFolderEntry,
  splitBareLocalPaths,
} from './local-files'
import type { ReplyFolderContext } from './local-images'

const ROOTS = ['/var/work/agents/a1/workspace', '/private/var/work/agents/a1/workspace']
const WIN_ROOTS = ['C:\\ProgramData\\loop-now\\agents\\a1\\workspace']

describe('local reply file link classification', () => {
  it('classifies Markdown link hrefs across external URLs, anchors, absolute/realPath, file://, Windows paths, relative paths, directories, and outside paths', () => {
    // External URLs, anchors, and non-file schemes stay on Markdown's normal path.
    expect(classifyMarkdownLinkHref('https://example.com/a.html', ROOTS)).toEqual({ kind: 'external' })
    expect(classifyMarkdownLinkHref('http://127.0.0.1:8780/app', ROOTS)).toEqual({ kind: 'external' })
    expect(classifyMarkdownLinkHref('mailto:a@example.com', ROOTS)).toEqual({ kind: 'external' })
    expect(classifyMarkdownLinkHref('#footnote-1', ROOTS)).toEqual({ kind: 'external' })
    expect(classifyMarkdownLinkHref('javascript:alert(1)', ROOTS)).toEqual({ kind: 'external' })

    // Absolute paths (path and realPath), file:// URLs, percent-encoded names, and query/fragment suffixes inside roots.
    expect(classifyMarkdownLinkHref('/var/work/agents/a1/workspace/pelican.html', ROOTS)).toEqual({
      kind: 'local', relativePath: 'pelican.html', trailingSlash: false, suffix: '',
    })
    expect(classifyMarkdownLinkHref('/private/var/work/agents/a1/workspace/site/index.html#scene2', ROOTS)).toEqual({
      kind: 'local', relativePath: 'site/index.html', trailingSlash: false, suffix: '#scene2',
    })
    expect(classifyMarkdownLinkHref('file:///var/work/agents/a1/workspace/my%20site/page.html?v=1#top', ROOTS)).toEqual({
      kind: 'local', relativePath: 'my site/page.html', trailingSlash: false, suffix: '?v=1#top',
    })
    expect(classifyMarkdownLinkHref('file://localhost/var/work/agents/a1/workspace/assets/', ROOTS)).toEqual({
      kind: 'local', relativePath: 'assets', trailingSlash: true, suffix: '',
    })

    // Relative paths and directory references inside the folder.
    expect(classifyMarkdownLinkHref('pelican-night-ride.html', ROOTS)).toEqual({
      kind: 'local', relativePath: 'pelican-night-ride.html', trailingSlash: false, suffix: '',
    })
    expect(classifyMarkdownLinkHref('./site/sub/../index.html', ROOTS)).toEqual({
      kind: 'local', relativePath: 'site/index.html', trailingSlash: false, suffix: '',
    })
    expect(classifyMarkdownLinkHref('assets/', ROOTS)).toEqual({
      kind: 'local', relativePath: 'assets', trailingSlash: true, suffix: '',
    })
    expect(classifyMarkdownLinkHref('.', ROOTS)).toEqual({ kind: 'local-dir', relativePath: '' })
    expect(classifyMarkdownLinkHref('./', ROOTS)).toEqual({ kind: 'local-dir', relativePath: '' })
    expect(classifyMarkdownLinkHref('/var/work/agents/a1/workspace', ROOTS)).toEqual({ kind: 'local-dir', relativePath: '' })
    expect(classifyMarkdownLinkHref('/var/work/agents/a1/workspace/', ROOTS)).toEqual({ kind: 'local-dir', relativePath: '' })

    // Windows drive paths and file:///C:/ URLs match regardless of separator or drive-letter case.
    expect(classifyMarkdownLinkHref('C:/ProgramData/loop-now/agents/a1/workspace/site/index.html', WIN_ROOTS)).toEqual({
      kind: 'local', relativePath: 'site/index.html', trailingSlash: false, suffix: '',
    })
    expect(classifyMarkdownLinkHref('c:\\ProgramData\\loop-now\\agents\\a1\\workspace\\pelican.html', WIN_ROOTS)).toEqual({
      kind: 'local', relativePath: 'pelican.html', trailingSlash: false, suffix: '',
    })
    expect(classifyMarkdownLinkHref('file:///C:/ProgramData/loop-now/agents/a1/workspace/page.html', WIN_ROOTS)).toEqual({
      kind: 'local', relativePath: 'page.html', trailingSlash: false, suffix: '',
    })

    // Outside paths and escapes are blocked, never navigating the console.
    expect(classifyMarkdownLinkHref('/etc/passwd', ROOTS)).toEqual({ kind: 'outside' })
    expect(classifyMarkdownLinkHref('/var/work/agents/a1/workspace-other/page.html', ROOTS)).toEqual({ kind: 'outside' })
    expect(classifyMarkdownLinkHref('/var/work/agents/a1/workspace/../other/secret.html', ROOTS)).toEqual({ kind: 'outside' })
    expect(classifyMarkdownLinkHref('file:///tmp/outside.html', ROOTS)).toEqual({ kind: 'outside' })
    expect(classifyMarkdownLinkHref('../secret.html', ROOTS)).toEqual({ kind: 'outside' })
    expect(classifyMarkdownLinkHref('site/../../secret.html', ROOTS)).toEqual({ kind: 'outside' })
    expect(classifyMarkdownLinkHref('D:\\other\\secret.html', WIN_ROOTS)).toEqual({ kind: 'outside' })
    expect(classifyMarkdownLinkHref('file:///C:/Windows/win.ini', WIN_ROOTS)).toEqual({ kind: 'outside' })

    // Absolute paths wait while folder roots are still loading, while relative paths resolve immediately.
    expect(classifyMarkdownLinkHref('/var/work/agents/a1/workspace/page.html', [], false)).toEqual({ kind: 'pending-root' })
    expect(classifyMarkdownLinkHref('file:///var/work/agents/a1/workspace/page.html', [], false)).toEqual({ kind: 'pending-root' })
    expect(classifyMarkdownLinkHref('page.html', [], false)).toEqual({
      kind: 'local', relativePath: 'page.html', trailingSlash: false, suffix: '',
    })
  })

  it('classifies inline-code paths without false positives on slash commands, URLs, or ordinary code', () => {
    expect(classifyInlineCodePath('/var/work/agents/a1/workspace/pelican.html', ROOTS)).toEqual({
      kind: 'local', relativePath: 'pelican.html', trailingSlash: false, certain: true,
    })
    expect(classifyInlineCodePath('file:///var/work/agents/a1/workspace/site/index.html', ROOTS)).toEqual({
      kind: 'local', relativePath: 'site/index.html', trailingSlash: false, certain: true,
    })
    expect(classifyInlineCodePath('./pelican.html', ROOTS)).toEqual({
      kind: 'local', relativePath: 'pelican.html', trailingSlash: false, certain: true,
    })
    expect(classifyInlineCodePath('pelican.html', ROOTS)).toEqual({
      kind: 'local', relativePath: 'pelican.html', trailingSlash: false, certain: false,
    })
    expect(classifyInlineCodePath('site/index.html', ROOTS)).toEqual({
      kind: 'local', relativePath: 'site/index.html', trailingSlash: false, certain: false,
    })
    expect(classifyInlineCodePath('C:\\ProgramData\\loop-now\\agents\\a1\\workspace\\page.html', WIN_ROOTS)).toEqual({
      kind: 'local', relativePath: 'page.html', trailingSlash: false, certain: true,
    })

    // Outside paths.
    expect(classifyInlineCodePath('/etc/passwd', ROOTS)).toEqual({ kind: 'outside' })
    expect(classifyInlineCodePath('/tmp/outside.html', ROOTS)).toEqual({ kind: 'outside' })
    expect(classifyInlineCodePath('file:///etc/hosts', ROOTS)).toEqual({ kind: 'outside' })
    expect(classifyInlineCodePath('../outside.html', ROOTS)).toEqual({ kind: 'outside' })
    expect(classifyInlineCodePath('C:\\Windows\\win.ini', WIN_ROOTS)).toEqual({ kind: 'outside' })

    // Ordinary code tokens, URLs, slash commands, API routes, and versions stay plain code.
    for (const token of ['/model', '/compact', '/api/v1/agents', '/p/demo/agents', 'https://example.com/a.html', 'npm run test', 'v0.3.5', 'GET/POST', 'useState', 'example.com']) {
      expect(classifyInlineCodePath(token, ROOTS), token).toEqual({ kind: 'none' })
    }
  })

  it('splits bare local paths in text across spaced roots, outside paths, relative files, and Chinese punctuation', () => {
    const spacedRoot = ['/Users/ljl/Library/Application Support/Loop Now/workspace']
    expect(
      splitBareLocalPaths(
        'Open (/Users/ljl/Library/Application Support/Loop Now/workspace/site/pelican.html), check ./notes.md, and never /etc/passwd.',
        spacedRoot,
      ),
    ).toEqual([
      { kind: 'text', value: 'Open (' },
      { kind: 'local', raw: '/Users/ljl/Library/Application Support/Loop Now/workspace/site/pelican.html', relativePath: 'site/pelican.html', trailingSlash: false, suffix: '', certain: true },
      { kind: 'text', value: '), check ' },
      { kind: 'local', raw: './notes.md', relativePath: 'notes.md', trailingSlash: false, suffix: '', certain: true },
      { kind: 'text', value: ', and never ' },
      { kind: 'outside', raw: '/etc/passwd' },
      { kind: 'text', value: '.' },
    ])

    // Sibling prefix does not accidentally match the folder root.
    expect(splitBareLocalPaths('Sibling /var/work/agents/a1/workspace-other/page.html stays outside.', ROOTS)).toEqual([
      { kind: 'text', value: 'Sibling ' },
      { kind: 'outside', raw: '/var/work/agents/a1/workspace-other/page.html' },
      { kind: 'text', value: ' stays outside.' },
    ])

    // Chinese prose directly before an ASCII relative filename or around a bare path keeps Chinese text intact.
    expect(splitBareLocalPaths('我写了pelican.html，外部是/tmp/secret.html。', ROOTS)).toEqual([ // check-english: zh (bare paths bounded by Chinese text and punctuation)
      { kind: 'text', value: '我写了' }, // check-english: zh (bare paths bounded by Chinese text and punctuation)
      { kind: 'local', raw: 'pelican.html', relativePath: 'pelican.html', trailingSlash: false, suffix: '', certain: false },
      { kind: 'text', value: '，外部是' }, // check-english: zh (bare paths bounded by Chinese text and punctuation)
      { kind: 'outside', raw: '/tmp/secret.html' },
      { kind: 'text', value: '。' }, // check-english: zh (bare paths bounded by Chinese text and punctuation)
    ])
  })

  it('probes raw HEAD and directory entries to distinguish files, directories, missing files, oversized files, and escaping symlinks', async () => {
    server.use(
      http.head(apiUrl('/agents/a1/folder/raw/*'), ({ request }) => {
        const path = new URL(request.url).pathname.split('/folder/raw/')[1] ?? ''
        if (path === 'site/index.html') return new HttpResponse(null, { status: 200 })
        if (path === 'report.pdf') return new HttpResponse(null, { status: 200, headers: { 'Content-Length': '1258291' } })
        if (path === 'empty.txt') return new HttpResponse(null, { status: 200, headers: { 'Content-Length': '0' } })
        if (path === 'big.mp4') return new HttpResponse(null, { status: 413 })
        if (path === 'assets' || path === 'escape.html') return new HttpResponse(null, { status: 422 })
        return new HttpResponse(null, { status: 404 })
      }),
    )

    const folder: ReplyFolderContext = {
      roots: ROOTS,
      contentURL: (rel) => apiUrl(`/agents/a1/folder/content?${new URLSearchParams({ path: rel })}`),
      rawURL: (rel) => apiUrl(`/agents/a1/folder/raw/${rel.split('/').map(encodeURIComponent).join('/')}`),
      folderLink: (rel) => `/p/demo/agents/a1/folder?${new URLSearchParams({ path: rel })}`,
      checkDirectory: async (rel) => (rel === 'assets' ? 'dir' : rel === 'missing-dir' ? 'missing' : 'not-dir'),
      maxBytes: 1024,
    }

    expect(await probeLocalFolderEntry(folder, 'site/index.html', false)).toEqual({ kind: 'file' })
    expect(await probeLocalFolderEntry(folder, 'report.pdf', false)).toEqual({ kind: 'file', size: 1258291 })
    expect(await probeLocalFolderEntry(folder, 'empty.txt', false)).toEqual({ kind: 'file', size: 0 })
    expect(await probeLocalFolderEntry(folder, 'assets', false)).toEqual({ kind: 'dir' })
    expect(await probeLocalFolderEntry(folder, 'assets', true)).toEqual({ kind: 'dir' })
    expect(await probeLocalFolderEntry(folder, 'missing-dir', true)).toEqual({ kind: 'missing' })
    expect(await probeLocalFolderEntry(folder, 'gone.html', false)).toEqual({ kind: 'missing' })
    expect(await probeLocalFolderEntry(folder, 'big.mp4', false)).toEqual({ kind: 'too-large' })
    expect(await probeLocalFolderEntry(folder, 'escape.html', false)).toEqual({ kind: 'outside' })
    expect(await probeLocalFolderEntry(folder, '', true)).toEqual({ kind: 'dir' })
  })
})
