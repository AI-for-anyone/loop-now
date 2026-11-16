import { describe, expect, it } from 'vitest'
import { enUS } from './en-US'
import { grouped, plural } from './format'
import { locale, resolveLocale } from './locale'
import { t } from './index'
import { zhCN } from './zh-CN'

// The Chinese table on its own (its punctuation, units and date formats): see src/zh-CN/i18n/zh-table.zh.test.ts.
describe('copy tables', () => {
  it('the main suite runs in the English UI (the Chinese UI: src/zh-CN)', () => {
    expect(locale).toBe('en-US')
    expect(t).toBe(enUS)
  })

  it('keys are type-checked, and function copy is built from its arguments', () => {
    // @ts-expect-error a string key that doesn't exist fails the type check
    expect(t.common.notExist).toBeUndefined()
    expect(t.common.save).toBe('Save')
    expect(t.chat.tooLong(65536)).toContain('64.0 KiB')
  })

  it('error codes and failure reasons have English text', () => {
    expect(t.errors.code.agent_busy).toBe('The Agent is busy')
    expect(t.chat.runError.pi_exited).toBe('Agent process exited unexpectedly')
  })

  it('numbers and plurals go to Intl with the locale', () => {
    expect(grouped('zh-CN', 1234567)).toBe('1,234,567')
    expect(plural('en-US', 1, { one: 'reply', other: 'replies' })).toBe('reply')
    expect(plural('en-US', 3, { one: 'reply', other: 'replies' })).toBe('replies')
    expect(plural('zh-CN', 1, { other: '条' })).toBe('条') // check-english: zh (a Chinese plural form, next to the English ones)
  })

  // The types already require the same shape; loose Record<string, string>
  // maps (failure reasons, thinking levels, etc.) are checked key by key here.
  it('the English table has the same keys as the Chinese one, and its functions take the same number of arguments', () => {
    const shape = (value: unknown, path: string, out: string[]) => {
      if (typeof value === 'function') out.push(`${path}()${value.length}`)
      else if (value && typeof value === 'object') for (const [key, child] of Object.entries(value)) shape(child, `${path}.${key}`, out)
      else out.push(path)
      return out
    }
    expect(shape(enUS, 't', [])).toEqual(shape(zhCN, 't', []))
  })

  it('UI language: ?lang= first, then the saved choice, then the browser language (Chinese for zh*, English otherwise)', () => {
    expect(resolveLocale('?lang=en-US', 'zh-CN', ['zh-CN'])).toBe('en-US')
    expect(resolveLocale('?lang=en', null, ['zh-CN'])).toBe('en-US')
    expect(resolveLocale('?lang=zh', 'en-US', ['en-US'])).toBe('zh-CN')
    expect(resolveLocale('?lang=fr', 'en-US', ['zh-CN'])).toBe('en-US')
    expect(resolveLocale('', 'zh-CN', ['en-GB'])).toBe('zh-CN')
    expect(resolveLocale('', 'ja', ['zh-TW', 'en'])).toBe('zh-CN')
    expect(resolveLocale('', null, ['fr-FR', 'zh-CN'])).toBe('en-US')
    expect(resolveLocale('', null, [])).toBe('en-US')
  })
})
