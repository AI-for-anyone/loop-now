import { describe, expect, it } from 'vitest'
import { t } from '@/i18n'
import { zhCN } from '@/i18n/zh-CN'
import { formatClock, formatCount, formatElapsed, formatMonthDay, formatStepDuration } from '@/i18n/zh-CN/format'

// The Chinese copy table itself. Its keys and the number of arguments of its functions match the English table: the type requires
// the same shape (Messages is built from the Chinese table and the English one `satisfies` it), and src/i18n/i18n.test.ts compares
// the two key by key, including the loose maps the type cannot pin down. What only the Chinese table does is checked here: copy
// functions joining with Chinese punctuation (“、”, “：”, “（）”), Chinese units, and Chinese date formats.
describe('the Chinese copy table', () => {
  it('is the table in use in the Chinese UI', () => {
    expect(t).toBe(zhCN)
    expect(t.common.save).toBe('保存')
  })

  it('error codes and failure reasons have Chinese text', () => {
    expect(t.errors.code.agent_busy).toBe('Agent 正忙')
    expect(t.chat.runError.pi_exited).toBe('Agent 进程异常退出')
  })

  it('copy functions join their arguments with Chinese punctuation', () => {
    expect(t.config.credentialEnvNames(['GH_TOKEN', 'GITHUB_TOKEN'])).toBe('注入的环境变量：GH_TOKEN、GITHUB_TOKEN')
    expect(t.overview.run.names(['开发助手', '审查员'], 5)).toBe('开发助手、审查员 等 5 个')
    expect(t.overview.run.names(['开发助手', '审查员'], 2)).toBe('开发助手、审查员')
    expect(t.consoleInbox.badge(2, 3)).toBe('2 个问题、3 条新消息')
    expect(t.consoleInbox.badge(0, 1)).toBe('1 条新消息')
    expect(t.plugins.detail.versionsLine([{ version: '1.0.0', current: true }, { version: '0.9.0', current: false }] as never)).toBe('已装版本：v1.0.0（当前）、v0.9.0')
    expect(t.stats.process.unpriced(['gpt-5', ''])).toBe('未计入没有单价的模型：gpt-5、未知模型')
    expect(t.common.unknown('beta')).toBe('未知（beta）')
    expect(t.errors.unknownStatus(502)).toBe('未知错误（HTTP 502）')
    expect(t.errors.withDetail('Agent 正忙', 'run 7f3a is still replying')).toBe('Agent 正忙：run 7f3a is still replying')
    expect(t.chat.failure('超时', 'context deadline exceeded')).toBe('失败：超时（context deadline exceeded）')
    expect(t.chat.tooLong(65536)).toBe('消息超过上限（64.0 KiB）')
    expect(t.timeline.toggle('9.4 s', '1,234')).toBe('运行记录 · 9.4 s · 1,234 token')
  })

  it('durations and counts use Chinese units', () => {
    expect(t.sleep.slept(30_000)).toBe('睡了 30 秒')
    expect(t.sleep.slept(5 * 60_000)).toBe('睡了 5 分钟')
    expect(t.sleep.slept(90 * 60_000)).toBe('睡了 1 小时 30 分')
    expect(formatElapsed(75_000)).toBe('1 分 15 秒')
    expect(formatStepDuration(125_000)).toBe('2 分 5 秒')
    expect(t.stats.wan(999)).toBe('999 token')
    expect(t.stats.wan(123_456)).toBe('12.3 万')
    expect(t.stats.wan(1_234_567)).toBe('123 万')
    expect(t.stats.clock(100)).toBe('1 分 40 秒')
    expect(formatCount(1234567)).toBe('1,234,567')
    expect(t.timeline.step.background_wait_timeout(10 * 60_000)).toBe('等后台命令时运行时 10 分钟没有任何消息，这一轮在这里结束')
  })

  it('dates and times use the Chinese formats with a 24-hour clock', () => {
    // Noon UTC is the same calendar day in every time zone the tests run in.
    // Numeric month and day: Intl writes 10/14 for zh-CN too (“10月14日” would need month: 'long').
    expect(formatMonthDay('2026-10-14T12:00:00Z')).toBe('10/14')
    const evening = new Date(2026, 9, 14, 20, 5, 9).toISOString()
    expect(formatClock(evening)).toBe('20:05:09')
    expect(formatClock(evening, false)).toBe('20:05')
  })
})
