import { describe, expect, it } from 'vitest'
import { enUS } from './en-US'
import { grouped, plural } from './format'
import { resolveLocale } from './locale'
import { t } from './index'
import { zhCN } from './zh-CN'

describe('文案表', () => {
  it('key 有类型检查，函数文案按参数生成', () => {
    // @ts-expect-error a string key that doesn't exist fails the type check
    expect(t.common.notExist).toBeUndefined()
    expect(t.common.save).toBe('保存')
    expect(t.chat.tooLong(65536)).toContain('64.0 KiB')
  })

  it('错误码与失败原因都有中文说明', () => {
    expect(t.errors.code.agent_busy).toBe('Agent 正忙')
    expect(t.chat.runError.pi_exited).toBe('Agent 进程异常退出')
  })

  it('数字与复数按 locale 交给 Intl', () => {
    expect(grouped('zh-CN', 1234567)).toBe('1,234,567')
    expect(plural('en-US', 1, { one: 'reply', other: 'replies' })).toBe('reply')
    expect(plural('en-US', 3, { one: 'reply', other: 'replies' })).toBe('replies')
    expect(plural('zh-CN', 1, { other: '条' })).toBe('条')
  })

  // The types already require the same shape; loose Record<string, string>
  // maps (failure reasons, thinking levels, etc.) are checked key by key here.
  it('英文文案表与中文的键一一对应，函数参数个数相同', () => {
    const shape = (value: unknown, path: string, out: string[]) => {
      if (typeof value === 'function') out.push(`${path}()${value.length}`)
      else if (value && typeof value === 'object') for (const [key, child] of Object.entries(value)) shape(child, `${path}.${key}`, out)
      else out.push(path)
      return out
    }
    expect(shape(enUS, 't', [])).toEqual(shape(zhCN, 't', []))
  })

  it('界面语言：?lang= 优先，其次是保存的选择，最后按浏览器语言（zh 开头用中文，其余用英文）', () => {
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
