import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { nameRuleError } from './names'

describe('nameRuleError', () => {
  it.each([
    ['前端/开发', 'Names can\'t contain "/" or "#"'],  // check-english: zh (names may be CJK; the rules count code points)
    ['开发#1', 'Names can\'t contain "/" or "#"'],  // check-english: zh (names may be CJK; the rules count code points)
    ['@开发', 'Names can\'t start with "@"'],  // check-english: zh (names may be CJK; the rules count code points)
    [' 开发', 'Names can\'t start or end with whitespace'],  // check-english: zh (names may be CJK; the rules count code points)
    ['开发　', 'Names can\'t start or end with whitespace'],  // check-english: zh (names may be CJK; the rules count code points)
    ['\u0085开发', 'Names can\'t contain control characters'],  // check-english: zh (names may be CJK; the rules count code points)
    ['开\u0007发', 'Names can\'t contain control characters'],  // check-english: zh (names may be CJK; the rules count code points)
    ['0199F2A4-7B1C-7E3A-9F00-1234567890AB', 'Names can\'t look like a UUID'],
    ['长'.repeat(101), 'Names can be at most 100 characters'],  // check-english: zh (names may be CJK; the rules count code points)
  ])('%j breaks the rules', (name, message) => {
    expect(nameRuleError(name)).toBe(message)
  })

  it.each(['开发助手', 'dev-bot', '开发 助手', 'a@b', '﻿助手', '😀'.repeat(60), '长'.repeat(100)])('%j follows the rules', (name) => {  // check-english: zh (names may be CJK; the rules count code points)
    expect(nameRuleError(name)).toBeUndefined()
  })
})

// Compare the local precheck's results code point by code point with the pattern and not.pattern of EntityName in the contract.
describe('matches EntityName in the contract', () => {
  const yaml = readFileSync(resolve(process.cwd(), '../api/openapi.yaml'), 'utf8')
  const block = yaml.slice(yaml.indexOf('    EntityName:'))
  const unquote = (raw: string) => JSON.parse(raw) as string
  const pattern = new RegExp(unquote(/\n {6}pattern: (".*")\n/.exec(block)![1]))
  const notPattern = new RegExp(unquote(/\n {6}not:\n {8}pattern: (".*")\n/.exec(block)![1]))
  const contractAccepts = (name: string) => [...name].length >= 1 && [...name].length <= 100 && pattern.test(name) && !notPattern.test(name)

  it('every BMP code point gives the same verdict as the contract at the start, in the middle, at the end or alone', () => {
    const mismatches: string[] = []
    for (let code = 0; code <= 0xffff; code += 1) {
      if (code >= 0xd800 && code <= 0xdfff) continue
      const ch = String.fromCharCode(code)
      for (const name of [ch, `${ch}名`, `名${ch}名`, `名${ch}`]) {  // check-english: zh (a CJK character around the code point)
        if ((nameRuleError(name) === undefined) !== contractAccepts(name)) mismatches.push(`U+${code.toString(16).toUpperCase().padStart(4, '0')} in ${JSON.stringify(name)}`)
      }
    }
    expect(mismatches.slice(0, 10)).toEqual([])
  })
})
