import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultAgentPath, makeAgent, makeMessage, makeMessagePage, makeRun } from '@/test/api'
import { renderChat } from '@/test/chat'

// 从概览的“压缩失败”点“查看压缩”：地址带 #run-<这次压缩>。压缩不是一轮，定位返回的那一页里没有它；
// 对话页用运行记录接口取到这次执行，在它发生的位置显示一张说明。
const at = (minute: number) => new Date(Date.UTC(2026, 10, 12, 8, minute)).toISOString()
const turn = (question: string, minute: number) => {
  const run = makeRun({ queuedAt: at(minute), startedAt: at(minute), runningAt: at(minute), endedAt: at(minute) })
  return [makeMessage({ text: question, createdAt: at(minute), run }), makeMessage({ role: 'assistant', text: `${question}的回答`, createdAt: at(minute), run })]
}
const compactionRun = (overrides: Partial<Schemas['Run']> = {}) =>
  makeRun({
    status: 'failed',
    errorCode: 'pi_error',
    errorMessage: 'compaction failed',
    queuedAt: at(5),
    startedAt: at(5),
    runningAt: at(5),
    endedAt: at(6),
    compaction: { instructions: '保留关键决定', outcome: null, skipReason: null, tokensBefore: null, estimatedTokensAfter: null },
    ...overrides,
  })

async function openCompaction(run: Schemas['Run'], timeline: () => Response = () => HttpResponse.json({ run, entries: [{ type: 'ended', at: run.endedAt, status: run.status, endReason: run.endReason, errorCode: run.errorCode }], usage: run.usage })) {
  const agent = makeAgent()
  const messages = [...turn('压缩前的问题', 1), ...turn('压缩后的问题', 9)]
  let timelineReads = 0
  const result = await renderChat({
    agent,
    path: `${defaultAgentPath(agent.id, 'chat')}#run-${run.id}`,
    handlers: [
      http.get(apiUrl(`/agents/${agent.id}/messages`), () => HttpResponse.json(makeMessagePage(messages, { turnsBefore: 3, turnsTotal: 5 }))),
      http.get(apiUrl(`/agents/${agent.id}/runs/${run.id}/timeline`), () => { timelineReads += 1; return timeline() }),
    ],
  })
  return { ...result, timelineReads: () => timelineReads }
}

const card = () => document.querySelector<HTMLElement>('[data-compaction-run]')

describe('定位到一次手动压缩', () => {
  it('在它发生的位置显示一张突出的说明：压缩失败、指令、原因，运行记录展开；它不算一轮', async () => {
    const run = compactionRun()
    await openCompaction(run)
    await waitFor(() => expect(card()).not.toBeNull())
    const note = card()!
    expect(note).toHaveAttribute('data-run-id', run.id)
    expect(note).toHaveAttribute('data-located')
    expect(note).toHaveClass('bg-tint-yellow')
    expect(within(note).getByText('定位到的手动压缩')).toHaveClass('sr-only')
    expect(note).toHaveTextContent('/compact 保留关键决定')
    expect(note).toHaveTextContent('手动压缩')
    expect(within(note).getByText('压缩失败')).toBeInTheDocument()
    expect(note).toHaveTextContent('Agent 报告错误：compaction failed')
    expect(note).toHaveTextContent('手动压缩不是对话里的一轮，没有消息')
    // 运行记录默认展开，复用同一次取到的时间线。
    const toggle = within(note).getByRole('button', { name: /^运行记录/ })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    // 排在压缩前后的两轮之间。
    const before = screen.getByText('压缩前的问题')
    const after = screen.getByText('压缩后的问题')
    expect(before.compareDocumentPosition(note) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(note.compareDocumentPosition(after) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // 不占编号与刻度：仍是两轮，编号接着 3 往下数。
    expect([...document.querySelectorAll('[data-turn-number]')].map((n) => n.textContent)).toEqual(['4', '5'])
    expect(within(screen.getByRole('navigation', { name: '对话轮次' })).getAllByRole('button')).toHaveLength(2)
  })

  it('压缩没有失败时照实写：压缩了写前后 token，没压缩写原因', async () => {
    const run = compactionRun({ status: 'completed', errorCode: null, errorMessage: null, compaction: { instructions: '', outcome: 'skipped', skipReason: 'too_small', tokensBefore: null, estimatedTokensAfter: null } })
    await openCompaction(run)
    await waitFor(() => expect(card()).not.toBeNull())
    expect(card()).toHaveTextContent('会话内容太少，不用压缩')
    expect(card()).toHaveTextContent(/\/compact(?! )/)
    expect(within(card()!).queryByText('压缩失败')).toBeNull()
  })

  it('定位的是一轮普通回复时不另取运行记录', async () => {
    const agent = makeAgent()
    const messages = turn('普通的问题', 1)
    let timelineReads = 0
    await renderChat({
      agent,
      path: `${defaultAgentPath(agent.id, 'chat')}#run-${messages[0].run.id}`,
      handlers: [
        http.get(apiUrl(`/agents/${agent.id}/messages`), () => HttpResponse.json(makeMessagePage(messages))),
        http.get(apiUrl(`/agents/${agent.id}/runs/:runId/timeline`), () => { timelineReads += 1; return HttpResponse.json({ error: { code: 'not_found', message: 'x' } }, { status: 404 }) }),
      ],
    })
    await waitFor(() => expect(document.querySelector('[data-located]')).not.toBeNull())
    // 普通回复的运行记录默认展开会读一次时间线；不该再为“找压缩”多读。
    await new Promise((resolve) => setTimeout(resolve, 50))
    expect(timelineReads).toBeLessThanOrEqual(1)
    expect(card()).toBeNull()
  })

  it('取到的执行不是手动压缩（或取不到）时不显示说明', async () => {
    const run = compactionRun({ compaction: undefined })
    const { timelineReads } = await openCompaction(run)
    await waitFor(() => expect(timelineReads()).toBe(1))
    await new Promise((resolve) => setTimeout(resolve, 30))
    expect(card()).toBeNull()
  })
})
