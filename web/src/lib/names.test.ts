import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { nameRuleError } from './names'

describe('nameRuleError', () => {
  it.each([
    ['前端/开发', '名称不能含“/”或“#”'],
    ['开发#1', '名称不能含“/”或“#”'],
    ['@开发', '名称不能以“@”开头'],
    [' 开发', '名称首尾不能有空白'],
    ['开发　', '名称首尾不能有空白'],
    ['\u0085开发', '名称不能含控制字符'],
    ['开\u0007发', '名称不能含控制字符'],
    ['0199F2A4-7B1C-7E3A-9F00-1234567890AB', '名称不能是 UUID 格式'],
    ['长'.repeat(101), '名称最长 100 个字符'],
  ])('%j 不符合规则', (name, message) => {
    expect(nameRuleError(name)).toBe(message)
  })

  it.each(['开发助手', 'dev-bot', '开发 助手', 'a@b', '﻿助手', '😀'.repeat(60), '长'.repeat(100)])('%j 符合规则', (name) => {
    expect(nameRuleError(name)).toBeUndefined()
  })
})

// 用契约中 EntityName 的 pattern 与 not.pattern 逐个码点比对本地预检的结果。
describe('与契约 EntityName 一致', () => {
  const yaml = readFileSync(resolve(process.cwd(), '../api/openapi.yaml'), 'utf8')
  const block = yaml.slice(yaml.indexOf('    EntityName:'))
  const unquote = (raw: string) => JSON.parse(raw) as string
  const pattern = new RegExp(unquote(/\n {6}pattern: (".*")\n/.exec(block)![1]))
  const notPattern = new RegExp(unquote(/\n {6}not:\n {8}pattern: (".*")\n/.exec(block)![1]))
  const contractAccepts = (name: string) => [...name].length >= 1 && [...name].length <= 100 && pattern.test(name) && !notPattern.test(name)

  it('每个 BMP 码点放在开头、中间、结尾或单独出现时，结论与契约相同', () => {
    const mismatches: string[] = []
    for (let code = 0; code <= 0xffff; code += 1) {
      if (code >= 0xd800 && code <= 0xdfff) continue
      const ch = String.fromCharCode(code)
      for (const name of [ch, `${ch}名`, `名${ch}名`, `名${ch}`]) {
        if ((nameRuleError(name) === undefined) !== contractAccepts(name)) mismatches.push(`U+${code.toString(16).toUpperCase().padStart(4, '0')} in ${JSON.stringify(name)}`)
      }
    }
    expect(mismatches.slice(0, 10)).toEqual([])
  })
})
