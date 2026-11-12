import { screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, makeMessage, makeMessagePage, makeRun, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'

// 对话页的轮次编号取自消息页的 turnsBefore、turnsTotal（契约 MessagePage）。
const turn = (question: string, minute: number) => {
  const time = new Date(Date.UTC(2026, 10, 12, 8, minute)).toISOString()
  const run = makeRun({ queuedAt: time, startedAt: time, runningAt: time, endedAt: time })
  return [makeMessage({ text: question, createdAt: time, run }), makeMessage({ role: 'assistant', text: `${question}的回答`, createdAt: time, run })]
}
const numbers = () => [...document.querySelectorAll('[data-turn-number]')].map((node) => node.textContent)
const total = () => screen.getByRole('navigation', { name: '对话轮次' }).querySelector('[data-ruler-total]')
const idle = (agentId: string) => ({ status: makeStatus(agentId), currentText: null, queuedRuns: [], queueItems: [] })

describe('对话页的轮次编号', () => {
  it('这一页之前有 24 轮时从 25 编号，底部是总轮数；更早的消息没加载也照样编号', async () => {
    await renderChat({ messages: [...turn('第一问', 1), ...turn('第二问', 2)], hasMore: true, page: { turnsBefore: 24, turnsTotal: 26 } })
    await waitFor(() => expect(numbers()).toEqual(['25', '26']))
    expect(total()).toHaveTextContent('26')
  })

  it('加载更早的一页后，用已加载的最早一页的计数接着编号', async () => {
    const older = [...turn('更早一', 1), ...turn('更早二', 2)]
    const newer = [...turn('较新一', 3), ...turn('较新二', 4)]
    const { user } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), ({ request }) =>
          new URL(request.url).searchParams.get('before')
            ? HttpResponse.json(makeMessagePage(older, { hasMore: true, turnsBefore: 10, turnsTotal: 14 }))
            : HttpResponse.json(makeMessagePage(newer, { hasMore: true, turnsBefore: 12, turnsTotal: 14 })),
        ),
      ],
    })
    await waitFor(() => expect(numbers()).toEqual(['13', '14']))
    await user.click(screen.getByRole('button', { name: '加载更早的消息' }))
    await waitFor(() => expect(numbers()).toEqual(['11', '12', '13', '14']))
    expect(total()).toHaveTextContent('14')
  })

  it('新发的消息、新来的回复在本地接着编号，总数跟着加，不等重新拉取', async () => {
    let reads = 0
    const history = [...turn('第一问', 1), ...turn('第二问', 2)]
    const { agent, emit } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/messages'), () => {
          reads += 1
          return HttpResponse.json(makeMessagePage(history, { hasMore: true, turnsBefore: 24, turnsTotal: 26 }))
        }),
      ],
    })
    await emit('snapshot', idle(agent.id))
    await waitFor(() => expect(numbers()).toEqual(['25', '26']))
    const readsBefore = reads
    const run = makeRun({ status: 'running', queuedAt: '2026-11-12T09:00:00Z', startedAt: '2026-11-12T09:00:00Z', runningAt: '2026-11-12T09:00:01Z', endedAt: null })
    await emit('message.created', { message: makeMessage({ text: '第三问', createdAt: '2026-11-12T09:00:00Z', run }) })
    await waitFor(() => expect(numbers()).toEqual(['25', '26', '27']))
    expect(total()).toHaveTextContent('27')
    expect(within(screen.getByRole('navigation', { name: '对话轮次' })).getAllByRole('button')).toHaveLength(3)
    expect(reads).toBe(readsBefore)
  })

  it('没有对话时不编号，也没有总数；第一轮从 1 开始', async () => {
    const { agent, emit } = await renderChat({ messages: [] })
    await emit('snapshot', idle(agent.id))
    expect(numbers()).toEqual([])
    const run = makeRun({ status: 'running', queuedAt: '2026-11-12T09:00:00Z', startedAt: '2026-11-12T09:00:00Z', runningAt: '2026-11-12T09:00:01Z', endedAt: null })
    await emit('message.created', { message: makeMessage({ text: '第一问', createdAt: '2026-11-12T09:00:00Z', run }) })
    await waitFor(() => expect(numbers()).toEqual(['1']))
  })
})

// 手动压缩（/compact）进行中：Agent 的当前执行是这次压缩，它不是一轮。
describe('手动压缩进行中', () => {
  it('不多出一轮：没有新的轮次块、编号和刻度，总数不变；忙碌状态照常显示', async () => {
    const { agent, emit } = await renderChat({ messages: [...turn('第一问', 1), ...turn('第二问', 2)], page: { turnsBefore: 24, turnsTotal: 26 }, hasMore: true })
    const compacting = makeRun({ status: 'running', queuedAt: '2026-11-12T09:00:00Z', startedAt: '2026-11-12T09:00:00Z', runningAt: '2026-11-12T09:00:01Z', endedAt: null, compaction: { instructions: '', outcome: null, skipReason: null, tokensBefore: null, estimatedTokensAfter: null } })
    await emit('snapshot', { status: makeStatus(agent.id, { busy: true, currentRun: compacting, processState: 'busy' }), currentText: '', queuedRuns: [], queueItems: [] })
    await screen.findByText(/Agent 正忙/)
    expect(numbers()).toEqual(['25', '26'])
    expect(document.querySelectorAll('[data-run-id]')).toHaveLength(2)
    expect(within(screen.getByRole('navigation', { name: '对话轮次' })).getAllByRole('button')).toHaveLength(2)
    expect(total()).toHaveTextContent('26')
    expect(screen.getByRole('button', { name: '停止当前回复' })).toBeInTheDocument()
  })
})
