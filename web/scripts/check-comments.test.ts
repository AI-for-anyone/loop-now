import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
// @ts-expect-error the script is an ESM module without type declarations
import { checkDirs, findChineseComments } from './check-comments.mjs'

const dirs: string[] = []

function fixture(files: Record<string, string>): string {
  const dir = mkdtempSync(join(tmpdir(), 'comments-'))
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

const lines = (path: string, source: string) => findChineseComments(path, source).map((hit: { line: number }) => hit.line)

describe('check-comments', () => {
  it('reports Chinese in line, block and JSDoc comments with their line numbers', () => {
    const source = ['// English is fine', '// 中文注释', 'const a = 1 /* 块注释 */', '/**', ' * 说明', ' */', 'export {}'].join('\n')
    expect(lines('a.ts', source)).toEqual([2, 3, 5])
  })

  it('reports comments inside JSX expressions', () => {
    expect(lines('a.tsx', 'export const A = () => (\n  <div>\n    {/* 这里 */}\n  </div>\n)\n')).toEqual([3])
  })

  it('ignores Chinese in strings, template literals and JSX text', () => {
    const source = "export const a = '保存'\nexport const b = `共 ${1} 条`\nexport const C = () => <p>取消</p>\n"
    expect(lines('a.tsx', source)).toEqual([])
  })

  it('allows quoted Chinese UI text but not full-width punctuation outside quotes', () => {
    expect(lines('a.ts', '// Shows the “不可撤销” (Irreversible) stamp\n// Matches 「保存」 and "删除"\nexport {}\n')).toEqual([])
    expect(lines('a.ts', '// Shows a stamp（not quoted）\nexport {}\n')).toEqual([1])
    expect(lines('a.ts', "// Don't hide 中文 between apostrophes, isn't it\nexport {}\n")).toEqual([1])
  })

  it('checks CSS, HTML, Go and hash comments but not their strings', () => {
    expect(lines('a.css', '/* 颜色 */\n.a { content: "中文"; }\n')).toEqual([1])
    expect(lines('a.html', '<!-- 页面 -->\n<style>\n  /* 样式 */\n</style>\n<p>正文</p>\n')).toEqual([1, 3])
    expect(lines('main.go', 'package main\n// 启动\nvar s = "中文 // 不是注释"\n')).toEqual([2])
    expect(lines('run.py', '# 运行\nprint("# 不是注释")\n')).toEqual([1])
    expect(lines('requirements.txt', '# 字体工具\nfonttools==4\n')).toEqual([1])
    expect(lines('npm24', '#!/bin/sh\n# 说明\necho "中文"\n')).toEqual([2])
  })

  it('walks directories, skipping node_modules, dist and generated files', () => {
    const dir = fixture({
      'src/a.ts': '// 中文\nexport {}\n',
      'src/schema.gen.ts': '// 生成的\nexport {}\n',
      'node_modules/x/index.js': '// 依赖\n',
      'dist/main.js': '// 产物\n',
      'README.md': '中文文档\n',
    })
    const problems = checkDirs([dir, join(dir, 'missing')])
    expect(problems.map((p: { file: string; line: number }) => [p.file.slice(dir.length + 1), p.line])).toEqual([['src/a.ts', 1]])
  })
})
