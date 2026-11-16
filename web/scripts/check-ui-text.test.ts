import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
// @ts-expect-error the script is an ESM module without type declarations
import { checkDir } from './check-ui-text.mjs'

const dirs: string[] = []

function fixture(files: Record<string, string>): string {
  const dir = mkdtempSync(join(tmpdir(), 'ui-text-'))
  dirs.push(dir)
  for (const [path, content] of Object.entries(files)) {
    mkdirSync(join(dir, path, '..'), { recursive: true })
    writeFileSync(join(dir, path), content)
  }
  return dir
}

afterEach(() => {
  for (const dir of dirs.splice(0)) rmSync(dir, { recursive: true, force: true })
})

describe('check-ui-text', () => {
  it('reports Chinese text written directly in a component, with its file', () => {
    const dir = fixture({ 'features/a/Button.tsx': 'export const B = () => <button>保存</button>\n' }) // check-english: zh (sample source with Chinese UI text)
    const problems = checkDir(dir)
    expect(problems).toHaveLength(1)
    expect(problems[0].file).toContain('Button.tsx')
    expect(problems[0].text).toBe('保存') // check-english: zh (the Chinese text the check reports)
  })

  it('checks strings and template literals', () => {
    const dir = fixture({ 'a.ts': "export const x = '删除'\nexport const y = `共 ${1} 条`\n" }) // check-english: zh (sample source with Chinese strings)
    expect(checkDir(dir).map((p: { line: number }) => p.line)).toEqual([1, 2])
  })

  it('allows Chinese comments, copy tables and test files', () => {
    const dir = fixture({
      'a.tsx': '// 这是注释\n/* 块注释 */\nexport const A = () => <div className="x">{t.save}</div>\n', // check-english: zh (sample source with Chinese comments)
      'i18n/zh-CN.ts': "export const zh = { save: '保存' }\n", // check-english: zh (sample Chinese copy table)
      'a.test.tsx': "it('显示保存', () => {})\n", // check-english: zh (sample test file with Chinese)
      'test/fixtures.ts': "export const name = '开发助手'\n", // check-english: zh (sample test fixture with Chinese)
    })
    expect(checkDir(dir)).toEqual([])
  })

  it('reports Chinese in the English copy table', () => {
    const dir = fixture({ 'i18n/en-US/common.ts': "export const common = { save: 'Save', cancel: '取消' }\n" }) // check-english: zh (sample English copy table holding Chinese)
    const problems = checkDir(dir)
    expect(problems).toHaveLength(1)
    expect(problems[0]).toMatchObject({ text: '取消', english: true }) // check-english: zh (the Chinese text the check reports)
  })
})
