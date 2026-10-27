import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, makeAgent, makeMessage, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'

type Entry = Schemas['TimelineEntry']

const at = (seconds: number) => new Date(Date.UTC(2026, 9, 12, 8, 0, 0) + seconds * 1000).toISOString()
const usage = (input: number, output: number, cacheRead = 0, cacheWrite = 0, calls = 1): Schemas['UsageTotals'] => ({
  calls,
  input,
  output,
  cacheRead,
  cacheWrite,
  total: input + output + cacheRead + cacheWrite,
})

const modelCall = (seconds: number, input: number, output: number): Entry => ({
  type: 'model_call',
  at: at(seconds),
  kind: 'assistant',
  provider: 'anthropic',
  model: 'claude-sonnet-5-5',
  responseModel: 'claude-sonnet-5-5-20261001',
  stopReason: 'toolUse',
  input,
  output,
  cacheRead: 0,
  cacheWrite: 0,
  reasoning: null,
  total: input + output,
  durationMs: 800,
})

const tool = (seconds: number, id: string, state: Schemas['ToolState'], durationMs: number | null, command?: string): Entry => ({
  type: 'tool',
  at: at(seconds),
  toolCallId: id,
  toolName: 'bash',
  state,
  endedAt: durationMs === null ? null : at(seconds + durationMs / 1000),
  durationMs,
  command,
})

function timelineHandler(agentId: string, build: () => Schemas['RunTimeline']) {
  const calls: string[] = []
  const handler = http.get(apiUrl(`/agents/${agentId}/runs/:runId/timeline`), ({ params }) => {
    calls.push(String(params.runId))
    return HttpResponse.json(build())
  })
  return { handler, calls }
}

// 回复中的运行记录展开项。
const recordToggle = () => screen.getByRole('button', { name: /^运行记录/ })

describe('运行记录时间线', () => {
  it('默认收起，展开后按顺序显示两次 bash 的实际命令、用时和结果，不展示输出，末尾是完成与合计', async () => {
    const agent = makeAgent()
    const run = makeRun({
      queuedAt: at(0),
      startedAt: at(0),
      runningAt: at(1),
      endedAt: at(9.4),
      usage: usage(1200, 34),
      model: { provider: 'anthropic', modelId: 'claude-sonnet-5-5' },
      configVersion: 7,
      tools: [
        { toolCallId: 't1', toolName: 'bash', state: 'started', at: at(2) },
        { toolCallId: 't1', toolName: 'bash', state: 'succeeded', at: at(2.4) },
        { toolCallId: 't2', toolName: 'bash', state: 'started', at: at(4) },
        { toolCallId: 't2', toolName: 'bash', state: 'failed', at: at(6) },
      ],
    })
    const entries: Entry[] = [
      { type: 'dispatched', at: at(0) },
      { type: 'phase', at: at(0), phase: 'waiting_slot' },
      { type: 'phase', at: at(0.064), phase: 'preparing' },
      { type: 'workspace_sync', at: at(0.1), result: 'succeeded', durationMs: 500, error: null, sources: [] },
      { type: 'phase', at: at(0.664), phase: 'launching' },
      { type: 'pi_reused', at: at(0.7), pid: 48213, processStartedAt: at(-600) },
      { type: 'running', at: at(1) },
      modelCall(1.5, 1000, 20),
      tool(2, 't1', 'succeeded', 400, 'pwd'),
      modelCall(3, 200, 14),
      tool(4, 't2', 'failed', 2000, 'printf "line one\\n"\nexit 1'),
      { type: 'ended', at: at(9.4), status: 'completed', endReason: null, errorCode: null },
    ]
    const timeline = timelineHandler(agent.id, () => ({ run, entries, usage: run.usage }))
    const { user } = await renderChat({
      agent,
      messages: [makeMessage({ text: '跑一下测试', run }), makeMessage({ role: 'assistant', text: '测试跑完了', run })],
      handlers: [timeline.handler],
    })
    const toggle = await waitFor(recordToggle)
    expect(toggle).toHaveTextContent('运行记录 · 9.4 s · 1,234 token')
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(timeline.calls).toEqual([])

    await user.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    const panel = document.getElementById(toggle.getAttribute('aria-controls')!)!
    await within(panel).findByText('运行记录')
    expect(timeline.calls).toEqual([run.id])
    expect(panel).toHaveTextContent(run.id)
    expect(panel).toHaveTextContent('model=anthropic / claude-sonnet-5-5 · 配置 v7 · 空闲时发送')
    expect(panel).toHaveTextContent('合计 9.4 s')

    const labels = within(panel)
      .getAllByRole('listitem')
      .filter((item) => item.hasAttribute('data-tone'))
      .map((item) => item.querySelector('.font-black')!.textContent)
    expect(labels).toEqual(['入队', '派发', '等待执行名额', '准备运行环境', '同步配置与文件夹', 'Pi 启动', '开始回复', '模型调用', '工具 bash', '模型调用', '工具 bash', '完成'])
    const bashSteps = within(panel).getAllByText('工具 bash').map((label) => label.closest('li')!)
    expect(bashSteps[0]).toHaveTextContent('400 ms')
    expect(bashSteps[0]).toHaveAttribute('data-tone', 'ok')
    expect(bashSteps[0].querySelector('code')?.textContent).toBe('pwd')
    expect(bashSteps[1]).toHaveTextContent('2.0 s')
    expect(bashSteps[1]).toHaveTextContent('state=failed')
    expect(bashSteps[1]).toHaveAttribute('data-tone', 'fail')
    expect(bashSteps[1].querySelector('code')?.textContent).toBe('printf "line one\\n"\nexit 1')
    expect(within(panel).getByText('Pi 启动').closest('li')).toHaveTextContent('复用常驻的 Pi 进程，PID 48213。')
    expect(within(panel).getByText('入队').closest('li')).toHaveTextContent('Agent 空闲，直接派发。')
    expect(panel).toHaveTextContent('token 用量')
    expect(panel).toHaveTextContent('输入 1,200')
    expect(panel).toHaveTextContent('模型调用 1 次')
    expect(panel).toHaveTextContent('回复正常结束。')
    expect(panel).not.toHaveTextContent(/args|arguments|output=|tools\[/i)
  })

  it('同步失败的回复：显示失败的来源与原因，没有“开始回复”', async () => {
    const agent = makeAgent()
    const run = makeRun({
      status: 'failed',
      errorCode: 'workspace_sync_failed',
      errorMessage: '克隆 repo 失败',
      queuedAt: at(0),
      startedAt: at(0),
      runningAt: null,
      endedAt: at(2),
    })
    const entries: Entry[] = [
      { type: 'dispatched', at: at(0) },
      { type: 'phase', at: at(0.1), phase: 'preparing' },
      {
        type: 'workspace_sync',
        at: at(0.2),
        result: 'failed',
        durationMs: 1700,
        error: '克隆 repo 失败',
        sources: [
          { path: 'docs', kind: 'files', outcome: 'files_synced', filesWritten: 2, filesDeleted: 1, error: null },
          { path: 'repo', kind: 'git', outcome: 'failed', filesWritten: null, filesDeleted: null, error: 'Authentication failed' },
        ],
      },
      { type: 'ended', at: at(2), status: 'failed', endReason: null, errorCode: 'workspace_sync_failed' },
    ]
    const timeline = timelineHandler(agent.id, () => ({ run, entries, usage: run.usage }))
    const { user } = await renderChat({ agent, messages: [makeMessage({ text: '你好', run })], handlers: [timeline.handler] })
    await user.click(await waitFor(recordToggle))
    const panel = document.getElementById(recordToggle().getAttribute('aria-controls')!)!
    const sync = (await within(panel).findByText('同步配置与文件夹')).closest('li')!
    expect(sync).toHaveTextContent('同步失败：克隆 repo 失败')
    expect(sync).toHaveTextContent('docs：资料已同步（写入 2 个，删除 1 个）')
    expect(sync).toHaveTextContent('repo：失败（Authentication failed）')
    expect(sync).toHaveAttribute('data-tone', 'fail')
    expect(within(panel).queryByText('开始回复')).toBeNull()
    expect(panel).toHaveTextContent('errorCode=workspace_sync_failed · errorMessage="克隆 repo 失败"')
    expect(panel).toHaveTextContent('失败：工作区同步失败（克隆 repo 失败）')
  })

  it('回复进行中展开后，又调用了一次工具：不需要重新展开就出现在时间线中', async () => {
    const agent = makeAgent()
    const run = makeRun({ status: 'running', queuedAt: at(0), startedAt: at(0), runningAt: at(1), endedAt: null })
    let entries: Entry[] = [{ type: 'dispatched', at: at(0) }, { type: 'running', at: at(1) }, tool(2, 't1', 'succeeded', 300)]
    const timeline = timelineHandler(agent.id, () => ({ run, entries, usage: run.usage }))
    const { user, emit } = await renderChat({
      agent,
      messages: [makeMessage({ text: '你好', run })],
      status: { busy: true, currentRun: run },
      handlers: [timeline.handler],
    })
    await emit('snapshot', { status: makeStatus(agent.id, { busy: true, currentRun: run, processState: 'busy' }), currentText: '', queuedRuns: [], queueItems: [] })
    const toggle = await waitFor(recordToggle)
    expect(toggle).toHaveTextContent(/^运行记录 · 已用 /)
    await user.click(toggle)
    const panel = document.getElementById(toggle.getAttribute('aria-controls')!)!
    await within(panel).findByText('工具 bash')
    expect(within(panel).queryByText('完成')).toBeNull()

    entries = [...entries, tool(5, 't2', 'started', null, 'sleep 1')]
    await emit('run.tool', { runId: run.id, toolCallId: 't2', toolName: 'bash', state: 'started', at: at(5) })
    await waitFor(() => expect(within(panel).getAllByText('工具 bash')).toHaveLength(2))
    const running = within(panel).getAllByText('工具 bash')[1].closest('li')!
    expect(running).toHaveTextContent('进行中')
    expect(running.querySelector('code')?.textContent).toBe('sleep 1')
    expect(running).toHaveAttribute('data-tone', 'warn')
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
  })

  it('本功能上线前的回复只有部分时间点，只显示已有内容', async () => {
    const agent = makeAgent()
    const run = makeRun({ status: 'interrupted', endReason: 'user_interrupt', queuedAt: at(0), startedAt: at(0), runningAt: null, endedAt: at(3) })
    const entries: Entry[] = [
      { type: 'dispatched', at: at(0) },
      { type: 'ended', at: at(3), status: 'interrupted', endReason: 'user_interrupt', errorCode: null },
    ]
    const timeline = timelineHandler(agent.id, () => ({ run, entries, usage: run.usage }))
    const { user } = await renderChat({ agent, messages: [makeMessage({ text: '你好', run })], handlers: [timeline.handler] })
    await user.click(await waitFor(recordToggle))
    const panel = document.getElementById(recordToggle().getAttribute('aria-controls')!)!
    expect(await within(panel).findByText('派发')).toBeInTheDocument()
    expect(within(panel).getAllByText('已打断').length).toBeGreaterThan(0)
    expect(panel).toHaveTextContent('endReason=user_interrupt')
    expect(within(panel).queryByRole('alert')).toBeNull()
  })
})
