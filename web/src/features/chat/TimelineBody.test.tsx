import { screen } from '@testing-library/react'
import { expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { renderWithQuery } from '@/test/render'
import { TimelineSteps } from './TimelineBody'
import { buildSteps } from './timeline-steps'

it('只提供任务真实时间线条目时不伪造常态入队/开始步骤，工具终态独立可读', () => {
  const entry: Schemas['TimelineTool'] = { type: 'tool', at: '2026-10-18T00:00:00Z', toolCallId: 'fixture-tool', toolName: 'fixture-tool', state: 'failed', endedAt: '2026-10-18T00:00:00Z', durationMs: null }
  const steps = buildSteps(null, [entry])
  expect(steps).toHaveLength(1); expect(steps[0].tone).toBe('fail'); expect(steps[0].duration).toBeNull()
  renderWithQuery(<TimelineSteps steps={steps} />)
  expect(screen.getByText(/fixture-tool/)).toBeVisible(); expect(screen.queryByText('消息入队')).toBeNull()
})

it('命令按代码文本渲染，保留多行并允许长命令换行，不解析 HTML', () => {
  const entry: Schemas['TimelineTool'] = { type: 'tool', at: '2026-10-22T00:00:00Z', toolCallId: 'bash-one', toolName: 'bash', state: 'started', endedAt: null, durationMs: null }
  const command = `printf '<img src=x onerror=alert(1)>'\n${'long-path/'.repeat(80)}`
  const steps = buildSteps(null, [{ ...entry, command }])
  const { container } = renderWithQuery(<TimelineSteps steps={steps} />)
  const code = container.querySelector('code')!
  expect(code.textContent).toBe(command)
  expect(code).toHaveClass('whitespace-pre-wrap', '[overflow-wrap:anywhere]', 'max-w-full', 'min-w-0')
  expect(container.querySelector('img')).toBeNull()
  expect(screen.getByText('工具 bash').closest('li')).toHaveAttribute('data-tone', 'warn')
  expect(screen.queryByText(/tools\[/)).toBeNull()
})

it('不把其他工具的字段伪装成 bash 命令', () => {
  const entry: Schemas['TimelineTool'] = { type: 'tool', at: '2026-10-22T00:00:00Z', toolCallId: 'read-one', toolName: 'read', command: 'not a bash command', state: 'succeeded', endedAt: null, durationMs: null }
  const { container } = renderWithQuery(<TimelineSteps steps={buildSteps(null, [entry])} />)
  expect(container.querySelector('code')).toBeNull()
  expect(screen.queryByText(/未记录命令/)).toBeNull()
})

it.each(['started', 'succeeded', 'failed'] as const)('旧 bash 记录缺命令时如实提示，保留 %s 状态', state => {
  const entry: Schemas['TimelineTool'] = { type: 'tool', at: '2026-10-22T00:00:00Z', toolCallId: 'old-bash', toolName: 'bash', state, endedAt: null, durationMs: null }
  const { container } = renderWithQuery(<TimelineSteps steps={buildSteps(null, [entry])} />)
  expect(screen.getByText(/未记录命令/)).toBeVisible()
  expect(container.querySelector('code')).toBeNull()
  expect(screen.getByText('工具 bash').closest('li')).toHaveAttribute('data-tone', state === 'succeeded' ? 'ok' : state === 'failed' ? 'fail' : 'warn')
  expect(screen.queryByText(/tools\[/)).toBeNull()
})

it('转为打断显示为一步，带时间与 whenBusy 的变化；没有这一条的旧记录照常显示', () => {
  const run = { queuedAt: '2026-10-24T08:00:00Z', whenBusy: 'interrupt' as const }
  const entries: Schemas['TimelineEntry'][] = [
    { type: 'queued', at: '2026-10-24T08:00:00Z', whenBusy: 'queue' },
    { type: 'promoted', at: '2026-10-24T08:00:05Z', from: 'queue', to: 'interrupt' },
    { type: 'dispatched', at: '2026-10-24T08:00:06Z' },
  ]
  const steps = buildSteps(run, entries)
  expect(steps.map(step => step.label)).toEqual(['入队', '已转为打断', '派发'])
  // 入队那一步保留发送时的排队方式。
  expect(steps[0].detail).toBe('Agent 正忙，排在普通排队消息末尾。')
  renderWithQuery(<TimelineSteps steps={steps} />)
  const promoted = screen.getByText('已转为打断').closest('li')!
  expect(promoted).toHaveAttribute('data-tone', 'hot')
  expect(promoted).toHaveTextContent('whenBusy=queue → interrupt')
  expect(promoted).toHaveTextContent(t.timeline.clock('2026-10-24T08:00:05Z'))
  expect(buildSteps(run, [entries[0], entries[2]]).map(step => step.label)).toEqual(['入队', '派发'])
})

it('Inbox 消息被取消、逐条转为打断（带消息 ID）与被人停止各显示为一步', () => {
  const messageId = '58000000-0000-7000-8000-000000000001'
  const entries: Schemas['TimelineEntry'][] = [
    { type: 'promoted', at: '2026-10-25T08:00:01Z', from: 'queue', to: 'interrupt', messageId },
    { type: 'message_cancelled', at: '2026-10-25T08:00:02Z', messageId },
    { type: 'abort_requested', at: '2026-10-25T08:00:03Z', byRunId: null, stop: true },
    { type: 'abort_requested', at: '2026-10-25T08:00:04Z', byRunId: null },
  ]
  const steps = buildSteps(null, entries)
  expect(steps.map(step => step.label)).toEqual(['已转为打断', '一条 Inbox 消息被取消', '被人停止', '收到打断消息'])
  expect(steps.map(step => step.field)).toEqual([`whenBusy=queue → interrupt · messageId=${messageId}`, `messageId=${messageId}`, 'abortRequestedAt · stop=true', 'abortRequestedAt · status=aborting'])
  expect(steps[2].tone).not.toBe('hot')
})
