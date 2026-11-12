import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { renderWithQuery } from '@/test/render'
import { TimelineSteps } from './TimelineBody'
import { buildSteps } from './timeline-steps'

type Entry = Schemas['TimelineEntry']
const at = (seconds: number) => new Date(Date.parse('2026-11-11T08:00:00Z') + seconds * 1000).toISOString()

describe('运行记录里的进程写出运行时', () => {
  it('Claude Code 的进程条目：启动阶段与进程几步都写 Claude Code；同一轮里停掉的旧 Pi 进程仍写 Pi', () => {
    const entries: Entry[] = [
      { type: 'pi_stopped', at: at(0), pid: 100, reason: 'config_changed', uptimeMs: 60_000, runtime: 'pi' },
      { type: 'phase', at: at(1), phase: 'launching' },
      { type: 'pi_started', at: at(2), pid: 200, startupMs: 900, reason: 'no_process', configVersion: 3, runtime: 'claude-code' },
      { type: 'pi_exited', at: at(5), pid: 200, uptimeMs: 3000, exit: 'signal: killed', stderrTail: '', runtime: 'claude-code' },
    ]
    const steps = buildSteps(null, entries)
    expect(steps.map((step) => step.label)).toEqual(['Pi 进程停止', 'Claude Code 启动', 'Claude Code 进程意外退出'])
    expect(steps[1].detail).toBe('启动新的 Claude Code 进程，PID 200（没有存活的进程）。')
  })

  it('复用常驻的 Claude Code 进程；之后平台停掉它也写 Claude Code', () => {
    const steps = buildSteps(null, [
      { type: 'phase', at: at(0), phase: 'launching' },
      { type: 'pi_reused', at: at(1), pid: 300, processStartedAt: at(-600), runtime: 'claude-code' },
      { type: 'pi_stopped', at: at(9), pid: 300, reason: 'abort_timeout', uptimeMs: 609_000, runtime: 'claude-code' },
    ])
    expect(steps.map((step) => step.label)).toEqual(['Claude Code 启动', 'Claude Code 进程停止'])
    expect(steps[0].detail).toBe('复用常驻的 Claude Code 进程，PID 300。')
  })

  it('旧记录没有 runtime：一律按 Pi', () => {
    const steps = buildSteps(null, [
      { type: 'phase', at: at(0), phase: 'launching' },
      { type: 'pi_started', at: at(1), pid: 1, startupMs: 10, reason: 'config_changed', configVersion: 1 },
      { type: 'pi_stopped', at: at(9), pid: 1, reason: 'idle_timeout', uptimeMs: 8000 },
    ])
    expect(steps.map((step) => step.label)).toEqual(['Pi 启动', 'Pi 进程停止'])
    expect(steps[0].detail).toBe('启动新的 Pi 进程，PID 1（配置修改后重启）。')
  })

  it('启动前就失败、没有进程条目：runtime_* 的失败说明是 Claude Code；其他失败按 Pi', () => {
    const failed = (errorCode: Schemas['RunErrorCode']): Entry[] => [
      { type: 'phase', at: at(0), phase: 'launching' },
      { type: 'ended', at: at(1), status: 'failed', endReason: null, errorCode },
    ]
    expect(buildSteps(null, failed('runtime_unavailable'))[0].label).toBe('Claude Code 启动')
    expect(buildSteps(null, failed('pi_start_failed'))[0].label).toBe('Pi 启动')
  })
})

describe('订阅额度（model_rate_limit）', () => {
  const entry = (info: Record<string, unknown>): Entry => ({ type: 'model_rate_limit', at: at(0), info: info as Record<string, never> })

  it('写出额度种类、状态与重置时间，字段原样放在等宽一栏', () => {
    const resetsAt = 1794382800
    const [step] = buildSteps(null, [entry({ status: 'allowed', rateLimitType: 'five_hour', resetsAt, utilization: 0.4 })])
    expect(step.label).toBe('订阅额度')
    expect(step.detail).toBe(`5 小时额度 · 可用 · ${t.timeline.detail.rateLimit.resetsAt(new Date(resetsAt * 1000).toISOString())}`)
    expect(step.field).toBe('rateLimitType=five_hour · status=allowed')
    expect(step.tone).toBe('neutral')
    renderWithQuery(<TimelineSteps steps={[step]} />)
    expect(screen.getByText('订阅额度').closest('li')).toHaveTextContent('5 小时额度 · 可用')
  })

  it('用完标红、快用完标黄；不认识的值原样显示', () => {
    expect(buildSteps(null, [entry({ status: 'rejected', rateLimitType: 'seven_day' })])[0]).toMatchObject({ detail: '7 天额度 · 已用完', tone: 'fail' })
    expect(buildSteps(null, [entry({ status: 'allowed_warning' })])[0]).toMatchObject({ detail: '快用完了', tone: 'warn' })
    expect(buildSteps(null, [entry({ status: 'paused', rateLimitType: 'monthly' })])[0]).toMatchObject({ detail: 'monthly · paused', field: 'rateLimitType=monthly · status=paused' })
  })

  it('字段缺失或类型不对时不出错：缺的不写，全缺时说明没有详情', () => {
    expect(buildSteps(null, [entry({})])[0]).toMatchObject({ detail: '没有给出额度详情', field: null, tone: 'neutral' })
    expect(buildSteps(null, [entry({ status: 7, rateLimitType: null, resetsAt: 'not a date' })])[0]).toMatchObject({ detail: '没有给出额度详情', field: null })
    const iso = '2026-11-11T13:00:00Z'
    expect(buildSteps(null, [entry({ resetsAt: iso })])[0].detail).toBe(t.timeline.detail.rateLimit.resetsAt(new Date(iso).toISOString()))
    expect(buildSteps(null, [entry({ resetsAt: 1794382800000 })])[0].detail).toBe(t.timeline.detail.rateLimit.resetsAt(new Date(1794382800000).toISOString()))
  })
})

// 平台命令（/new、/reload、/resume）会停掉空闲的进程，旧记录里还有 /export html 用完即停的临时进程。
describe('平台命令停止进程的原因', () => {
  it.each([
    ['session_reset', '重开会话（/new）后停止'],
    ['console_reload', '重新加载（/reload）时停止'],
    ['session_resumed', '恢复会话（/resume）后停止'],
    ['console_export', '导出会话用完后停止'],
  ] as const)('%s 写成“%s”', (reason, wording) => {
    const [step] = buildSteps(null, [{ type: 'pi_stopped', at: at(0), pid: 100, reason, uptimeMs: 60_000, runtime: 'pi' }])
    expect(step.label).toBe('Pi 进程停止')
    expect(step.detail).toContain(wording)
    expect(step.detail).not.toContain(reason)
  })
})
