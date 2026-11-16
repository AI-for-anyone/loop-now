import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
// @ts-expect-error the script is an ESM module without type declarations
import { checkDirs, findChineseTestStrings, isTestFile } from './check-test-strings.mjs'

const dirs: string[] = []

function fixture(files: Record<string, string>): string {
  const dir = mkdtempSync(join(tmpdir(), 'test-strings-'))
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

const lines = (path: string, source: string) => findChineseTestStrings(path, source).map((hit: { line: number }) => hit.line)

// check-english: zh (sample test sources with Chinese, for the check to find or to allow)
const zh = {
  literals: ["it('保存', () => {", "  expect(x).toBe(`共 ${n} 条`)", '  expect(y).toMatch(/^删除/)', '  render(<p>取消</p>)', "  expect(z).toBe('full stop。')", '})'].join('\n'),
  comment: "// 中文注释\nexpect(a).toBe('Save')\n",
  sameLine: "expect(a).toBe('中文') // check-english: zh (a CJK name)\nexpect(b).toBe(\n  '多行', // check-english: zh (multibyte truncation)\n)\n",
  endLine: 'const c = `第一行\n第二行` // check-english: zh (multibyte text)\n',
  previousLine: "it('x', () => {\n  // check-english: zh (a CJK name)\n  expect(a).toBe('中文')\n})\n",
  noReason: "expect(a).toBe('中文') // check-english: zh\nexpect(b).toBe('中文') // check-english: zh ()\n",
  statement: "// check-english: zh (data in both languages)\nconst table = {\n  zh: '保存',\n  en: 'Save',\n}\nconst other = '取消'\n",
  test: "it('x', () => expect(a).toBe('保存'))\n",
}

describe('check-test-strings', () => {
  it('covers test files, files in test directories and the web/scripts browser checks, TS and JS only', () => {
    for (const path of ['web/src/features/chat/Chat.test.tsx', 'web/src/test/api.ts', 'web/scripts/lark-login-app.mjs', 'web/scripts/integration/tasks/app-browser-verify.mjs', 'desktop/src/shell.test.ts', 'plugins/host/test/fixtures/pi-offline-provider.ts']) {
      expect(isTestFile(path), path).toBe(true)
    }
    for (const path of ['web/src/features/chat/Chat.tsx', 'web/src/api/schema.gen.ts', 'web/src/test/README.md', 'web/scripts/run-sleep-app.py', 'web/scripts/check-comments.mjs', 'desktop/src/strings.ts', 'desktop/test/fakeserver/main.go']) {
      expect(isTestFile(path), path).toBe(false)
    }
  })

  it('reports Chinese in strings, each template literal part, regular expressions and JSX text, but not in comments', () => {
    expect(lines('a.test.tsx', zh.literals)).toEqual([1, 2, 2, 3, 4, 5])
    expect(lines('a.test.ts', zh.comment)).toEqual([])
  })

  it('allows a literal marked with a reason on its first or last line, not on the line before or without a reason', () => {
    expect(lines('a.test.ts', zh.sameLine)).toEqual([])
    expect(lines('a.test.ts', zh.endLine)).toEqual([])
    expect(lines('a.test.ts', zh.previousLine)).toEqual([3])
    expect(lines('a.test.ts', zh.noReason)).toEqual([1, 2])
  })

  it('allows a whole top-level statement marked in its leading comment, and only that statement', () => {
    expect(lines('a.test.ts', zh.statement)).toEqual([6])
  })

  it('walks directories: skips the Chinese supplement, listed exceptions and files that are not tests', () => {
    const root = fixture({
      'web/src/features/a.test.ts': zh.test,
      'web/src/features/a.ts': zh.test,
      'web/src/zh-CN/features/a.zh.test.ts': zh.test,
      'web/scripts/old-app.mjs': zh.test,
      'web/scripts/new-app.mjs': zh.test,
      'web/node_modules/x/a.test.js': zh.test,
      'plugins/sdk/test/a.test.ts': zh.test,
    })
    const problems = checkDirs([join(root, 'web'), join(root, 'desktop'), join(root, 'plugins')], { root, exceptions: new Map([['web/scripts/old-app.mjs', 'needs a real account']]) })
    expect(problems.map((p: { file: string; line: number }) => [p.file.slice(root.length + 1), p.line])).toEqual([
      ['web/scripts/new-app.mjs', 1],
      ['web/src/features/a.test.ts', 1],
      ['plugins/sdk/test/a.test.ts', 1],
    ])
  })
})
