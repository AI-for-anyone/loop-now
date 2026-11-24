import { describe, expect, it } from 'vitest'
import { runErrorText } from '@/features/chat/run-status-text'

// Chinese puts spaces around a Latin runtime name, and none around “运行时” (the runtime) when the runtime isn't known.
describe("a runtime's failures in Chinese", () => {
  it('spaces a named runtime off and writes an unknown one as “运行时” without spaces', () => {
    expect(runErrorText('runtime_login_required', 'Codex')).toBe('本机的 Codex 没有登录，或登录已失效：在本机终端重新登录后再试')
    expect(runErrorText('runtime_login_required', null)).toBe('本机的运行时没有登录，或登录已失效：在本机终端重新登录后再试')
    expect(runErrorText('runtime_unavailable', null)).toBe('本机的运行时用不了：没找到它、读不出版本，或版本太旧')
    expect(runErrorText('runtime_start_failed', null)).toBe('运行时没能启动')
    expect(runErrorText('runtime_exited', 'Codex')).toBe('Codex 进程在这一轮中意外退出')
    expect(runErrorText('runtime_error', null)).toBe('运行时报告这一轮出错')
  })
})
