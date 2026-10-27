import { act, screen, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { useTaskMessages, taskKeys } from '@/api/tasks'
import { renderRoutes, createTestQueryClient } from '@/test/render'
import { makeTaskMessage, taskId, nodeId, oldNodeId } from '@/test/tasks'
import { FakeEventSource } from '@/test/fake-event-source'
import { server } from '@/test/server'
import { apiUrl } from '@/test/api'
import { taskMessageProjection, taskMessageRows } from './message-projection'
import { useTaskLive } from './useTaskLive'
import { TaskMessageBubble } from './TaskMessageBubble'

function Probe() {
  useTaskLive(taskId, false)
  const messages = useTaskMessages(taskId, nodeId)
  return <ol>{messages.data?.retainedMessages.map(message => <li key={message.id} data-message-id={message.id}><TaskMessageBubble message={message} agent={{ id: 'agent', name: '当前任务Agent' }} /></li>)}</ol>
}
function stream() { return FakeEventSource.instances.find(source => source.url.endsWith(`/tasks/${taskId}/events`))! }

it('历史GET悬挂时两次全文SSE已更新稳定气泡；迟到HTTP不抹掉较短的新正文', async () => {
  let release!: () => void, reads = 0
  const gate = new Promise<void>(resolve => { release = resolve })
  let current = { ...makeTaskMessage(nodeId, '旧HTTP正文'), status: 'running' as const }
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), async () => {
    reads++; const captured = current
    if (reads === 1) await gate
    return HttpResponse.json({ messages: [captured], nextCursor: null })
  }))
  const env = renderRoutes([{ path: '/', element: <Probe /> }], '/')
  await waitFor(() => expect(reads).toBe(1))
  act(() => { stream().emit('message.created', { ...current, text: '首先收到的很长实时全文' }, '9'); stream().emit('message.created', { ...current, text: '短正文' }, '10') })
  expect(await screen.findByText('短正文')).toBeVisible(); expect(screen.queryByText('旧HTTP正文')).toBeNull()
  const original = document.querySelector('[data-message-id]')
  current = { ...current, text: '短正文' }; await act(async () => { release(); await gate })
  await waitFor(() => expect(env.client.getQueryState(taskKeys.messages(taskId, nodeId))?.fetchStatus).toBe('idle')); expect(reads).toBe(1); expect(screen.getByText('短正文')).toBeVisible(); expect(document.querySelector('[data-message-id]')).toBe(original)
  expect(document.querySelectorAll('[data-message-id]')).toHaveLength(1)
  act(() => window.dispatchEvent(new Event('focus'))); await waitFor(() => expect(reads).toBe(2)); expect(screen.getByText('短正文')).toBeVisible()
})

it('HTTP终态之后迟到活动全文不恢复生成，重连快照不把消息当齐全或抹掉', async () => {
  const ended = makeTaskMessage(nodeId, '已结束的全文')
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), () => HttpResponse.json({ messages: [ended], nextCursor: null })))
  renderRoutes([{ path: '/', element: <Probe /> }], '/')
  await screen.findByText('已结束的全文')
  act(() => { stream().emit('message.created', { ...ended, status: 'running', text: '迟到活动正文' }, '20'); stream().interrupt(); stream().open() })
  expect(screen.getByText('已结束的全文')).toBeVisible(); expect(screen.queryByText('迟到活动正文')).toBeNull(); expect(screen.queryByText('生成中')).toBeNull()
})

describe('任务消息投影的顺序与范围', () => {
  it('十进制重复/倒序去重，同ID不可换run/role；新消息不按文本猜重', () => {
    const projection = taskMessageProjection(createTestQueryClient()), running = { ...makeTaskMessage(), status: 'running' as const }
    expect(projection.message(taskId, '9', running)).toBe(true)
    expect(projection.message(taskId, '10', { ...running, text: '新正文' })).toBe(true)
    expect(projection.message(taskId, '9', running)).toBe(false)
    expect(projection.message(taskId, '10', running)).toBe(false)
    expect(projection.message(taskId, '11', { ...running, runId: 'foreign-turn' })).toBe(false)
    expect(projection.message(taskId, '11', { ...running, role: 'user' })).toBe(false)
    expect(projection.message(taskId, '12', { ...running, id: 'another-message', text: '新正文' })).toBe(true)
    expect(projection.messages(taskId, nodeId).map(row => row.text)).toEqual(['新正文', '新正文'])
  })
  it('HTTP请求起点而非正文长度决定新旧，新的权威读取可校准且部分页不删除旧ID', () => {
    const projection = taskMessageProjection(createTestQueryClient()), message = { ...makeTaskMessage(), status: 'running' as const }
    const before = projection.beginRead(taskId, nodeId)
    projection.message(taskId, '1', { ...message, text: '更短' })
    expect(projection.reconcile(before, [{ ...message, text: '更长的旧内容' }])[0].text).toBe('更短')
    const fresh = projection.beginRead(taskId, nodeId)
    expect(projection.reconcile(fresh, [{ ...message, text: 'fresh-authority' }])[0].text).toBe('fresh-authority')
    projection.reconcile(projection.beginRead(taskId, nodeId), [])
    expect(projection.messages(taskId, nodeId)).toHaveLength(1)
  })
  it('新的连接代次挡住上次连接HTTP，跨任务/nodeRun及visit不串', () => {
    const projection = taskMessageProjection(createTestQueryClient()), running = { ...makeTaskMessage(), status: 'running' as const }
    projection.message(taskId, '1', running)
    const oldRead = projection.beginRead(taskId, nodeId); projection.connection(taskId)
    expect(projection.reconcile(oldRead, [{ ...running, text: '旧连接HTTP' }])[0].text).toBe(running.text)
    projection.message('other-task', '2', { ...running, text: '其他任务' })
    projection.message(taskId, '3', { ...running, nodeRunId: oldNodeId, text: '旧visit' })
    expect(projection.messages(taskId, nodeId).map(row => row.text)).toEqual([running.text])
    expect(taskMessageRows([running], projection.messages(taskId, oldNodeId), nodeId)).toEqual([running])
  })
  it('SSE接收终态后更大ID的活动不得复活，terminal全文仍可安全校准', () => {
    const projection = taskMessageProjection(createTestQueryClient()), terminal = makeTaskMessage()
    projection.message(taskId, '10', terminal)
    expect(projection.message(taskId, '11', { ...terminal, status: 'running' })).toBe(false)
    expect(projection.message(taskId, '12', { ...terminal, text: '最终校准' })).toBe(true)
    expect(projection.messages(taskId, nodeId)[0].text).toBe('最终校准')
  })
})
