import { describe, expect, it } from 'vitest'
import { ancestorsOf, canEdit, decodeText, encodeText, folderPath, linksOutside, nameProblem, within } from './folder-source'

const bytes = (...parts: (string | number[])[]) => {
  const chunks = parts.map((part) => (typeof part === 'string' ? new TextEncoder().encode(part) : new Uint8Array(part)))
  const out = new Uint8Array(chunks.reduce((sum, chunk) => sum + chunk.length, 0))
  let at = 0
  for (const chunk of chunks) { out.set(chunk, at); at += chunk.length }
  return out.buffer
}

describe('text detection and original format', () => {
  it('strips the BOM, turns CRLF into LF and records the original format; restores it when writing back', () => {
    const decoded = decodeText(bytes([0xef, 0xbb, 0xbf], '# 标题\r\n- 一行\r\n'))! // check-english: zh (BOM and CRLF round trip of non-ASCII text)
    expect(decoded).toEqual({ text: '# 标题\n- 一行\n', format: { bom: true, crlf: true } }) // check-english: zh (BOM and CRLF round trip of non-ASCII text)
    expect(encodeText(decoded.text + '- 新行\n', decoded.format)).toBe('﻿# 标题\r\n- 一行\r\n- 新行\r\n') // check-english: zh (BOM and CRLF round trip of non-ASCII text)
    expect(decodeText(bytes('plain\n'))).toEqual({ text: 'plain\n', format: { bom: false, crlf: false } })
  })

  it('treats a file as binary when its first 8 KiB contain NUL or are not valid UTF-8', () => {
    expect(decodeText(bytes([0x7f, 0x45, 0x4c, 0x46, 0x00, 0x01]))).toBeNull()
    expect(decodeText(bytes([0xff, 0xfe, 0x41]))).toBeNull()
  })
})

it('name and path checks, and when online editing is allowed', () => {
  expect([nameProblem(''), nameProblem('a/b'), nameProblem('..'), nameProblem('notes.md')]).toEqual(['empty', 'slash', 'dots', null])
  expect([folderPath(' /docs/archive/ '), folderPath(''), folderPath('a/../b'), folderPath('a//b')]).toEqual(['docs/archive', '', null, null])
  const limits = { maxBytes: 1024, extensions: ['.md', '.go'] }
  expect([canEdit('AGENTS.md', 10, limits), canEdit('main.go', 2048, limits), canEdit('Makefile', 10, limits), canEdit('a.md', 10, undefined)]).toEqual([true, false, false, false])
  expect(ancestorsOf('web/src/api/queries.ts')).toEqual(['web', 'web/src', 'web/src/api'])
  expect([within('web/src', 'web'), within('web', 'web'), within('website', 'web')]).toEqual([true, true, false])
  expect([linksOutside('a.md', '/etc/hosts'), linksOutside('a.md', '../x'), linksOutside('docs/a.md', '../../x'), linksOutside('docs/a.md', '../handoff/notes.md'), linksOutside('a.md', './docs/../b.md')]).toEqual([true, true, true, false, false])
})
