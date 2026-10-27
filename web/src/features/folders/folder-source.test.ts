import { describe, expect, it } from 'vitest'
import { ancestorsOf, canEdit, decodeText, encodeText, folderPath, linksOutside, nameProblem, within } from './folder-source'

const bytes = (...parts: (string | number[])[]) => {
  const chunks = parts.map((part) => (typeof part === 'string' ? new TextEncoder().encode(part) : new Uint8Array(part)))
  const out = new Uint8Array(chunks.reduce((sum, chunk) => sum + chunk.length, 0))
  let at = 0
  for (const chunk of chunks) { out.set(chunk, at); at += chunk.length }
  return out.buffer
}

describe('文本判断与原有格式', () => {
  it('去掉 BOM、把 CRLF 统一为 LF，并记下原有格式；写回时照原样恢复', () => {
    const decoded = decodeText(bytes([0xef, 0xbb, 0xbf], '# 标题\r\n- 一行\r\n'))!
    expect(decoded).toEqual({ text: '# 标题\n- 一行\n', format: { bom: true, crlf: true } })
    expect(encodeText(decoded.text + '- 新行\n', decoded.format)).toBe('﻿# 标题\r\n- 一行\r\n- 新行\r\n')
    expect(decodeText(bytes('plain\n'))).toEqual({ text: 'plain\n', format: { bom: false, crlf: false } })
  })

  it('前 8 KiB 里有 NUL 或不是合法的 UTF-8 时当作二进制', () => {
    expect(decodeText(bytes([0x7f, 0x45, 0x4c, 0x46, 0x00, 0x01]))).toBeNull()
    expect(decodeText(bytes([0xff, 0xfe, 0x41]))).toBeNull()
  })
})

it('名称与路径校验、在线编辑的条件', () => {
  expect([nameProblem(''), nameProblem('a/b'), nameProblem('..'), nameProblem('notes.md')]).toEqual(['empty', 'slash', 'dots', null])
  expect([folderPath(' /docs/archive/ '), folderPath(''), folderPath('a/../b'), folderPath('a//b')]).toEqual(['docs/archive', '', null, null])
  const limits = { maxBytes: 1024, extensions: ['.md', '.go'] }
  expect([canEdit('AGENTS.md', 10, limits), canEdit('main.go', 2048, limits), canEdit('Makefile', 10, limits), canEdit('a.md', 10, undefined)]).toEqual([true, false, false, false])
  expect(ancestorsOf('web/src/api/queries.ts')).toEqual(['web', 'web/src', 'web/src/api'])
  expect([within('web/src', 'web'), within('web', 'web'), within('website', 'web')]).toEqual([true, true, false])
  expect([linksOutside('a.md', '/etc/hosts'), linksOutside('a.md', '../x'), linksOutside('docs/a.md', '../../x'), linksOutside('docs/a.md', '../handoff/notes.md'), linksOutside('a.md', './docs/../b.md')]).toEqual([true, true, true, false, false])
})
