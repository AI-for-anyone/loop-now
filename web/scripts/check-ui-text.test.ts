import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
// @ts-expect-error 脚本是无类型声明的 ESM 模块
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
  it('指出组件中直接书写的中文文字及其文件', () => {
    const dir = fixture({ 'features/a/Button.tsx': 'export const B = () => <button>保存</button>\n' })
    const problems = checkDir(dir)
    expect(problems).toHaveLength(1)
    expect(problems[0].file).toContain('Button.tsx')
    expect(problems[0].text).toBe('保存')
  })

  it('检查字符串与模板字符串', () => {
    const dir = fixture({ 'a.ts': "export const x = '删除'\nexport const y = `共 ${1} 条`\n" })
    expect(checkDir(dir).map((p: { line: number }) => p.line)).toEqual([1, 2])
  })

  it('允许中文注释、文案表与测试文件', () => {
    const dir = fixture({
      'a.tsx': '// 这是注释\n/* 块注释 */\nexport const A = () => <div className="x">{t.save}</div>\n',
      'i18n/zh-CN.ts': "export const zh = { save: '保存' }\n",
      'a.test.tsx': "it('显示保存', () => {})\n",
      'test/fixtures.ts': "export const name = '开发助手'\n",
    })
    expect(checkDir(dir)).toEqual([])
  })
})
