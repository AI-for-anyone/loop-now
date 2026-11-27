import { describe, expect, it } from 'vitest'
import {
  classifyFencedPath,
  classifyFileCardKind,
  fileCardAppearance,
  formatFileCardSize,
} from './file-cards'

const ROOTS = ['/var/work/agents/a1/workspace', '/private/var/work/agents/a1/workspace']
const WIN_ROOTS = ['C:\\ProgramData\\loop-now\\agents\\a1\\workspace']

describe('file card kind, color, and glyph classification', () => {
  it('maps extensions to the expected kinds, icon colors, and glyphs', () => {
    expect(fileCardAppearance('pelican-night-ride.html')).toEqual({ kind: 'webpage', color: '#7c3aed', glyph: 'code' })
    expect(fileCardAppearance('site/INDEX.HTM')).toEqual({ kind: 'webpage', color: '#7c3aed', glyph: 'code' })

    expect(fileCardAppearance('night-market-report.pdf')).toEqual({ kind: 'pdf', color: '#ef4444', glyph: 'pdf' })

    expect(fileCardAppearance('README.md')).toEqual({ kind: 'markdown', color: '#2563eb', glyph: 'doc' })
    expect(fileCardAppearance('docs/guide.markdown')).toEqual({ kind: 'markdown', color: '#2563eb', glyph: 'doc' })
    expect(fileCardAppearance('notes.txt')).toEqual({ kind: 'text', color: '#2563eb', glyph: 'doc' })
    expect(fileCardAppearance('server.log')).toEqual({ kind: 'text', color: '#2563eb', glyph: 'doc' })
    expect(fileCardAppearance('proposal.docx')).toEqual({ kind: 'document', color: '#2563eb', glyph: 'doc' })

    expect(fileCardAppearance('night-market-sales-2026-11.csv')).toEqual({ kind: 'table', color: '#10a37f', glyph: 'table' })
    expect(fileCardAppearance('metrics.tsv')).toEqual({ kind: 'table', color: '#10a37f', glyph: 'table' })
    expect(fileCardAppearance('budget.xlsx')).toEqual({ kind: 'table', color: '#10a37f', glyph: 'table' })

    expect(fileCardAppearance('src/app.ts')).toEqual({ kind: 'code', color: '#475569', glyph: 'code' })
    expect(fileCardAppearance('main.go')).toEqual({ kind: 'code', color: '#475569', glyph: 'code' })
    expect(fileCardAppearance('config.json')).toEqual({ kind: 'code', color: '#475569', glyph: 'code' })
    expect(fileCardAppearance('scripts/deploy.sh')).toEqual({ kind: 'code', color: '#475569', glyph: 'code' })

    expect(fileCardAppearance('build-output.zip')).toEqual({ kind: 'archive', color: '#64748b', glyph: 'file' })
    expect(fileCardAppearance('release.tar.gz')).toEqual({ kind: 'archive', color: '#64748b', glyph: 'file' })
    expect(fileCardAppearance('backup.7z')).toEqual({ kind: 'archive', color: '#64748b', glyph: 'file' })

    expect(fileCardAppearance('Makefile')).toEqual({ kind: 'file', color: '#64748b', glyph: 'file' })
    expect(fileCardAppearance('data.bin')).toEqual({ kind: 'file', color: '#64748b', glyph: 'file' })
    expect(classifyFileCardKind('.env')).toBe('file')
  })
})

describe('file card size formatting', () => {
  it('formats bytes in ChatGPT-style B, KB, MB, and GB units', () => {
    expect(formatFileCardSize(0)).toBe('0 B')
    expect(formatFileCardSize(-10)).toBe('0 B')
    expect(formatFileCardSize(512)).toBe('512 B')
    expect(formatFileCardSize(1023)).toBe('1023 B')
    expect(formatFileCardSize(1024)).toBe('1 KB')
    expect(formatFileCardSize(3 * 1024)).toBe('3 KB')
    expect(formatFileCardSize(18 * 1024)).toBe('18 KB')
    expect(formatFileCardSize(96 * 1024)).toBe('96 KB')
    expect(formatFileCardSize(1024 * 1024 - 1)).toBe('1 MB')
    expect(formatFileCardSize(Math.round(1.2 * 1024 * 1024))).toBe('1.2 MB')
    expect(formatFileCardSize(Math.round(2.4 * 1024 * 1024))).toBe('2.4 MB')
    expect(formatFileCardSize(1024 * 1024 * 1024)).toBe('1 GB')
    expect(formatFileCardSize(Math.round(1.5 * 1024 * 1024 * 1024))).toBe('1.5 GB')
  })
})

describe('fenced code block single-path detection', () => {
  it('recognizes a fenced block whose whole content is one non-image file path inside the folder', () => {
    expect(classifyFencedPath('/var/work/agents/a1/workspace/pelican-night-ride.html\n', ROOTS, true, 'text')).toEqual({
      kind: 'file',
      relativePath: 'pelican-night-ride.html',
      certain: true,
    })
    expect(classifyFencedPath('  file:///private/var/work/agents/a1/workspace/docs/report.pdf  \n', ROOTS, true, null)).toEqual({
      kind: 'file',
      relativePath: 'docs/report.pdf',
      certain: true,
    })
    expect(classifyFencedPath('./night-market-sales-2026-11.csv\n', ROOTS, true, 'text')).toEqual({
      kind: 'file',
      relativePath: 'night-market-sales-2026-11.csv',
      certain: true,
    })
    expect(classifyFencedPath('pelican-night-ride.html\n', ROOTS, true, null)).toEqual({
      kind: 'file',
      relativePath: 'pelican-night-ride.html',
      certain: false,
    })
    expect(classifyFencedPath('C:\\ProgramData\\loop-now\\agents\\a1\\workspace\\build.zip\n', WIN_ROOTS, true, 'text')).toEqual({
      kind: 'file',
      relativePath: 'build.zip',
      certain: true,
    })
  })

  it('keeps multi-line blocks, shell commands, images, directories, outside paths, HTTP URLs, and mermaid diagrams as code', () => {
    expect(classifyFencedPath('/var/work/agents/a1/workspace/a.html\n/var/work/agents/a1/workspace/b.html\n', ROOTS)).toEqual({ kind: 'none' })
    expect(classifyFencedPath('/var/work/agents/a1/workspace/deploy.sh --prod\n', ROOTS, true, 'bash')).toEqual({ kind: 'none' })
    expect(classifyFencedPath('/var/work/agents/a1/workspace/run.sh | tee out.log\n', ROOTS, true, 'bash')).toEqual({ kind: 'none' })
    expect(classifyFencedPath('/var/work/agents/a1/workspace/shots/preview.png\n', ROOTS, true, 'text')).toEqual({ kind: 'none' })
    expect(classifyFencedPath('/var/work/agents/a1/workspace/assets/\n', ROOTS, true, 'text')).toEqual({ kind: 'none' })
    expect(classifyFencedPath('/var/work/agents/a1/workspace\n', ROOTS, true, 'text')).toEqual({ kind: 'none' })
    expect(classifyFencedPath('/etc/passwd\n', ROOTS, true, 'text')).toEqual({ kind: 'none' })
    expect(classifyFencedPath('../outside.html\n', ROOTS, true, 'text')).toEqual({ kind: 'none' })
    expect(classifyFencedPath('https://example.com/pelican.html\n', ROOTS, true, 'text')).toEqual({ kind: 'none' })
    expect(classifyFencedPath('pelican.html', ROOTS, true, 'mermaid')).toEqual({ kind: 'none' })
    expect(classifyFencedPath('/var/work/agents/a1/workspace/pelican.html', [], false, 'text')).toEqual({ kind: 'none' })
  })
})
