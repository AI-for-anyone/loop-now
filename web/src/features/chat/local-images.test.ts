import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl } from '@/test/api'
import { server } from '@/test/server'
import {
  classifyMarkdownImageSource,
  isReplyImagePath,
  loadLocalFolderImage,
  mimeTypeForImagePath,
  normalizeRelativePath,
  resolveAbsoluteLocalImage,
  splitBareLocalImagePaths,
  stripFileUrl,
} from './local-images'

const ROOTS = ['/var/work/agents/a1/workspace', '/private/var/work/agents/a1/workspace']

describe('local reply image path resolution', () => {
  it('recognizes the six supported image extensions case-insensitively and rejects others', () => {
    for (const name of ['a.png', 'dir/b.JPG', 'c.jpeg', 'd.GIF', 'e.WebP', 'f.svg']) {
      expect(isReplyImagePath(name)).toBe(true)
    }
    expect(mimeTypeForImagePath('concept.SVG')).toBe('image/svg+xml')
    expect(mimeTypeForImagePath('photo.jpg')).toBe('image/jpeg')
    for (const bad of ['notes.md', 'archive.tar.gz', 'data.png.bak', '.png', 'noext']) {
      expect(isReplyImagePath(bad)).toBe(false)
    }
  })

  it('normalizes relative paths and blocks escapes above the folder root', () => {
    expect(normalizeRelativePath('assets/preview.png')).toBe('assets/preview.png')
    expect(normalizeRelativePath('./assets/./sub/../preview.png')).toBe('assets/preview.png')
    expect(normalizeRelativePath('../outside.png')).toBeNull()
    expect(normalizeRelativePath('assets/../../outside.png')).toBeNull()
    expect(normalizeRelativePath('/abs/path.png')).toBeNull()
    expect(normalizeRelativePath('')).toBeNull()
  })

  it('unwraps POSIX and Windows file:// URLs with percent-decoding', () => {
    expect(stripFileUrl('file:///Users/a/my%20pic.png')).toBe('/Users/a/my pic.png')
    expect(stripFileUrl('file://localhost/var/work/a.svg#frag')).toBe('/var/work/a.svg')
    expect(stripFileUrl('file:///C:/ProgramData/loop-now/ws/a.png')).toBe('C:/ProgramData/loop-now/ws/a.png')
    expect(stripFileUrl('https://example.com/a.png')).toBeNull()
  })

  it('resolves absolute and file:// paths against both path and realPath, rejecting outside paths and .. escapes', () => {
    expect(resolveAbsoluteLocalImage('/var/work/agents/a1/workspace/assets/preview.png', ROOTS)).toBe('assets/preview.png')
    expect(resolveAbsoluteLocalImage('/private/var/work/agents/a1/workspace/assets/preview.SVG', ROOTS)).toBe('assets/preview.SVG')
    expect(resolveAbsoluteLocalImage('file:///var/work/agents/a1/workspace/assets/my%20shot.webp', ROOTS)).toBe('assets/my shot.webp')
    // Traversal out of the folder is normalized before matching and rejected.
    expect(resolveAbsoluteLocalImage('/var/work/agents/a1/workspace/../other/secret.png', ROOTS)).toBeNull()
    expect(resolveAbsoluteLocalImage('/etc/passwd.png', ROOTS)).toBeNull()
    // Sibling prefix with the same starting characters must not match.
    expect(resolveAbsoluteLocalImage('/var/work/agents/a1/workspace-other/pic.png', ROOTS)).toBeNull()
    // Non-image files inside the folder do not match.
    expect(resolveAbsoluteLocalImage('/var/work/agents/a1/workspace/notes.md', ROOTS)).toBeNull()
    // Windows drive paths match regardless of separator or drive letter case.
    expect(resolveAbsoluteLocalImage('c:/ProgramData/loop-now/ws/shots/a.png', ['C:\\ProgramData\\loop-now\\ws'])).toBe('shots/a.png')
  })

  it('classifies Markdown image sources into external, local, and blocked', () => {
    expect(classifyMarkdownImageSource('https://example.com/a.png', ROOTS)).toEqual({ kind: 'external' })
    expect(classifyMarkdownImageSource('data:image/png;base64,AA==', ROOTS)).toEqual({ kind: 'external' })
    expect(classifyMarkdownImageSource('javascript:alert(1)', ROOTS)).toEqual({ kind: 'external' })
    expect(classifyMarkdownImageSource('assets/preview.png', ROOTS)).toEqual({ kind: 'local', relativePath: 'assets/preview.png' })
    expect(classifyMarkdownImageSource('./assets/my%20pic.JPG', ROOTS)).toEqual({ kind: 'local', relativePath: 'assets/my pic.JPG' })
    expect(classifyMarkdownImageSource('/private/var/work/agents/a1/workspace/diagram.svg', ROOTS)).toEqual({ kind: 'local', relativePath: 'diagram.svg' })
    expect(classifyMarkdownImageSource('file:///var/work/agents/a1/workspace/a.gif', ROOTS)).toEqual({ kind: 'local', relativePath: 'a.gif' })
    expect(classifyMarkdownImageSource('../outside.png', ROOTS)).toEqual({ kind: 'blocked' })
    expect(classifyMarkdownImageSource('/tmp/outside.png', ROOTS)).toEqual({ kind: 'blocked' })
    expect(classifyMarkdownImageSource('file:///tmp/outside.png', ROOTS)).toEqual({ kind: 'blocked' })
    expect(classifyMarkdownImageSource('assets/readme.md', ROOTS)).toEqual({ kind: 'blocked' })
  })

  it('splits bare local image paths in text while respecting punctuation, spaces in roots, and non-boundaries', () => {
    const spacedRoot = ['/Users/ljl/Library/Application Support/Loop Now/workspace']
    expect(
      splitBareLocalImagePaths(
        'See (/Users/ljl/Library/Application Support/Loop Now/workspace/shots/a.png) and /Users/ljl/Library/Application Support/Loop Now/workspace/b.svg.',
        spacedRoot,
      ),
    ).toEqual([
      { kind: 'text', value: 'See (' },
      { kind: 'image', raw: '/Users/ljl/Library/Application Support/Loop Now/workspace/shots/a.png', relativePath: 'shots/a.png' },
      { kind: 'text', value: ') and ' },
      { kind: 'image', raw: '/Users/ljl/Library/Application Support/Loop Now/workspace/b.svg', relativePath: 'b.svg' },
      { kind: 'text', value: '.' },
    ])

    // .png.bak, prefixed paths, and .. escapes stay untouched as plain text.
    const unchanged = 'Ignore /var/work/agents/a1/workspace/a.png.bak, /other/var/work/agents/a1/workspace/a.png, and /var/work/agents/a1/workspace/../x.png'
    expect(splitBareLocalImagePaths(unchanged, ROOTS)).toEqual([{ kind: 'text', value: unchanged }])

    // Chinese punctuation adjacent to a bare path stays outside the matched path.
    expect(splitBareLocalImagePaths('预览图：/var/work/agents/a1/workspace/preview.png，请看。', ROOTS)).toEqual([ // check-english: zh (bare path bounded by Chinese punctuation)
      { kind: 'text', value: '预览图：' }, // check-english: zh (bare path bounded by Chinese punctuation)
      { kind: 'image', raw: '/var/work/agents/a1/workspace/preview.png', relativePath: 'preview.png' },
      { kind: 'text', value: '，请看。' }, // check-english: zh (bare path bounded by Chinese punctuation)
    ])
  })

  it('loads image bytes via the folder content endpoint, wraps SVG with image/svg+xml, and rejects 404/422 or files over maxBytes', async () => {
    const svgBytes = '<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>'
    server.use(
      http.get(apiUrl('/agents/a1/folder/content'), ({ request }) => {
        const path = new URL(request.url).searchParams.get('path')
        if (path === 'ok.svg') return new HttpResponse(svgBytes, { headers: { 'Content-Type': 'application/octet-stream' } })
        if (path === 'huge-header.png') return new HttpResponse(new Uint8Array(10), { headers: { 'Content-Type': 'application/octet-stream', 'Content-Length': '500' } })
        if (path === 'huge-body.png') return new HttpResponse(new Uint8Array(256), { headers: { 'Content-Type': 'application/octet-stream' } })
        if (path === 'symlink-out.png') return new HttpResponse(null, { status: 422 })
        return new HttpResponse(null, { status: 404 })
      }),
    )

    const loaded = await loadLocalFolderImage(apiUrl('/agents/a1/folder/content?path=ok.svg'), 'ok.svg', 1024)
    expect(loaded).not.toBeNull()
    expect(loaded!.url).toMatch(/^(?:data:image\/svg\+xml|blob:)/)
    loaded!.revoke()

    expect(await loadLocalFolderImage(apiUrl('/agents/a1/folder/content?path=huge-header.png'), 'huge-header.png', 100)).toBeNull()
    expect(await loadLocalFolderImage(apiUrl('/agents/a1/folder/content?path=huge-body.png'), 'huge-body.png', 100)).toBeNull()
    expect(await loadLocalFolderImage(apiUrl('/agents/a1/folder/content?path=symlink-out.png'), 'symlink-out.png', 1024)).toBeNull()
    expect(await loadLocalFolderImage(apiUrl('/agents/a1/folder/content?path=missing.png'), 'missing.png', 1024)).toBeNull()
  })
})
