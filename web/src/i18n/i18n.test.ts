import { describe, expect, it } from 'vitest'
import { grouped, plural } from './format'
import { t } from './index'

describe('文案表', () => {
  it('key 有类型检查，函数文案按参数生成', () => {
    // @ts-expect-error 不存在的文案 key 无法通过类型检查
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
})
