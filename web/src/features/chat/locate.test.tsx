import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { apiUrl, errorBody, makeAgent, makeMessage, makeRun } from '@/test/api'
import { renderChat } from '@/test/chat'
import { renderRoutes } from '@/test/render'

// 300 条消息（150 轮）的对话历史，按 before、after、aroundRun 与 limit 分页，与服务端规则一致。
function history(agentId: string) {
  const messages: Schemas['Message'][] = []
  for (let i = 0; i < 150; i++) {
    const time = new Date(Date.UTC(2026, 9, 1) + i * 60_000).toISOString()
    const run = makeRun({ queuedAt: time, startedAt: time, runningAt: time, endedAt: time })
    messages.push(makeMessage({ text: `问题 ${i}`, createdAt: time, run }), makeMessage({ role: 'assistant', text: `回答 ${i}`, createdAt: time, run }))
  }
  // 每轮两条消息：这一页之前的轮次数是完整排在它前面的轮次（一轮被页边界切开时，这一轮算在这一页）。
  const page = (start: number, end: number): Schemas['MessagePage'] => ({
    messages: messages.slice(start, end),
    hasMore: start > 0,
    hasMoreAfter: end < messages.length,
    turnsBefore: Math.floor(start / 2),
    turnsTotal: messages.length / 2,
  })
  const handler = http.get(apiUrl(`/agents/${agentId}/messages`), ({ request }) => {
    const params = new URL(request.url).searchParams
    const limit = Number(params.get('limit') ?? 50)
    const before = params.get('before')
    const after = params.get('after')
    const aroundRun = params.get('aroundRun')
    if (aroundRun) {
      const index = messages.findIndex((m) => m.run.id === aroundRun)
      if (index < 0) return HttpResponse.json(errorBody('not_found'), { status: 404 })
      const start = Math.max(0, index - limit / 2)
      return HttpResponse.json(page(start, Math.min(messages.length, start + limit)))
    }
    if (before) {
      const index = messages.findIndex((m) => m.id === before)
      return HttpResponse.json(page(Math.max(0, index - limit), index))
    }
    if (after) {
      const index = messages.findIndex((m) => m.id === after) + 1
      return HttpResponse.json(page(index, Math.min(messages.length, index + limit)))
    }
    return HttpResponse.json(page(Math.max(0, messages.length - limit), messages.length))
  })
  const timeline = http.get(apiUrl(`/agents/${agentId}/runs/:runId/timeline`), ({ params }) => {
    const run = messages.find((m) => m.run.id === params.runId)!.run
    return HttpResponse.json({ run, entries: [{ type: 'ended', at: run.endedAt, status: 'completed', endReason: null, errorCode: null }], usage: run.usage })
  })
  return { messages, handlers: [handler, timeline] }
}

const turnIds = () => [...document.querySelectorAll('[data-run-id]')].map((el) => el.getAttribute('data-run-id'))
const located = () => document.querySelector<HTMLElement>('[data-located]')

async function openLocated(index: number) {
  const agent = makeAgent()
  const data = history(agent.id)
  // 第 index 条消息（从 1 数起）对应的回复。
  const target = data.messages[index - 1].run.id
  const result = await renderChat({ agent, handlers: data.handlers, path: `/agents/${agent.id}/chat#run-${target}` })
  await waitFor(() => expect(located()).not.toBeNull())
  return { ...result, data, target }
}

describe('从概览定位到回复', () => {
  it('定位时照常编号（用这一页之前的轮次数）；还没加载到最新时，尺子底部的总数用接口给的总轮数', async () => {
    await openLocated(40)
    // 第 n 轮写的是“问题 n-1”“回答 n-1”。这一页从一轮的中间开始（第一块只有回复）、到一轮的中间结束，编号照样对得上。
    const blocks = [...document.querySelectorAll<HTMLElement>('[data-run-id]')]
    expect(blocks.length).toBeLessThan(150)
    for (const block of [blocks[0], blocks[blocks.length - 1]]) {
      const index = Number(/(?:问题|回答) (\d+)/.exec(block.textContent ?? '')![1])
      expect(block.querySelector('[data-turn-number]')).toHaveTextContent(String(index + 1))
    }
    expect(screen.getByRole('navigation', { name: '对话轮次' }).querySelector('[data-ruler-total]')).toHaveTextContent('150')
  })

  it('定位到很早的回复：显示它并展开运行记录；向下能连续加载到最新消息，没有重复或缺失', async () => {
    const { data, target } = await openLocated(20)
    const turn = located()!
    expect(turn).toHaveAttribute('data-run-id', target)
    expect(turn).toHaveTextContent('回答 9')
    expect(within(turn).getByText('定位到的回复')).toBeInTheDocument()
    const toggle = within(turn).getByRole('button', { name: /^运行记录/ })
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(await within(turn).findByText('回复正常结束。')).toBeInTheDocument()
    expect(screen.getByText('正在查看定位的回复')).toBeInTheDocument()

    for (let i = 0; i < 10 && screen.queryByRole('button', { name: '加载更新的消息' }); i++) {
      // 页面上有上百轮对话时 user-event 的指针检查很慢，这里直接触发点击。
      fireEvent.click(screen.getByRole('button', { name: '加载更新的消息' }))
      await waitFor(() => expect(screen.queryByText('正在加载更新的消息…')).toBeNull())
    }
    expect(screen.queryByRole('button', { name: '加载更新的消息' })).toBeNull()
    const ids = turnIds()
    expect(ids).toHaveLength(150)
    expect(new Set(ids).size).toBe(150)
    expect(ids).toEqual(data.messages.filter((m) => m.role === 'user').map((m) => m.run.id))
    // 逐页加载到 150 轮、每轮渲染 Markdown 与运行记录，整套测试并行时较慢。
  }, 20_000)

  it('回到最新：显示最新的一页，之后像平常一样', async () => {
    const { user, data, router } = await openLocated(20)
    await user.click(screen.getByRole('button', { name: '回到最新' }))
    await waitFor(() => expect(router.state.location.hash).toBe(''))
    await waitFor(() => expect(turnIds().at(-1)).toBe(data.messages.at(-1)!.run.id))
    expect(turnIds()).toHaveLength(25)
    expect(located()).toBeNull()
    expect(screen.queryByText('正在查看定位的回复')).toBeNull()
  })

  it('地址中的回复不属于这个 Agent：提示找不到，并显示最新的对话', async () => {
    const agent = makeAgent()
    const data = history(agent.id)
    await renderChat({ agent, handlers: data.handlers, path: `/agents/${agent.id}/chat#run-01890a5d-ac96-774b-bcce-b302099a8057` })
    expect(await screen.findByText('找不到这条回复，下面是最新的对话。')).toBeInTheDocument()
    await waitFor(() => expect(turnIds().at(-1)).toBe(data.messages.at(-1)!.run.id))
    expect(located()).toBeNull()
    expect(screen.queryByRole('alert', { name: /加载失败/ })).toBeNull()
  })

  it('锚点格式不对时同样提示找不到，不发出定位请求', async () => {
    const agent = makeAgent()
    const data = history(agent.id)
    await renderChat({ agent, handlers: data.handlers, path: `/agents/${agent.id}/chat#run-not-a-run` })
    expect(await screen.findByText('找不到这条回复，下面是最新的对话。')).toBeInTheDocument()
  })

  it('尚未加载到最新时，新消息只提示，不插进对话流', async () => {
    const { emit } = await openLocated(20)
    const before = turnIds()
    const run = makeRun({ status: 'running', endedAt: null })
    await emit('message.created', { message: makeMessage({ text: '刚发的新消息', run }) })
    await emit('run.updated', { run })
    expect(await screen.findByText('有新消息')).toBeInTheDocument()
    expect(screen.queryByText('刚发的新消息')).toBeNull()
    expect(turnIds()).toEqual(before)
  })

  it('刷新后仍定位到同一条回复', async () => {
    const { target, agent, data, unmount } = await openLocated(40)
    unmount()
    renderRoutes(routes, `/agents/${agent.id}/chat#run-${target}`)
    await waitFor(() => expect(located()).toHaveAttribute('data-run-id', target))
    expect(located()).toHaveTextContent('回答 19')
    expect(data.messages[39].text).toBe('回答 19')
  })
})
