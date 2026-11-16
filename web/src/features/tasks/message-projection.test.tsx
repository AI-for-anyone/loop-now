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
  return <ol>{messages.data?.retainedMessages.map(message => <li key={message.id} data-message-id={message.id}><TaskMessageBubble message={message} agent={{ id: 'agent', name: 'Current task Agent' }} /></li>)}</ol>
}
function stream() { return FakeEventSource.instances.find(source => source.url.endsWith(`/tasks/${taskId}/events`))! }

it('while the history GET hangs, two full-text SSE events update one stable bubble; a late HTTP response does not wipe the shorter newer text', async () => {
  let release!: () => void, reads = 0
  const gate = new Promise<void>(resolve => { release = resolve })
  let current = { ...makeTaskMessage(nodeId, 'old HTTP text'), status: 'running' as const }
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), async () => {
    reads++; const captured = current
    if (reads === 1) await gate
    return HttpResponse.json({ messages: [captured], nextCursor: null })
  }))
  const env = renderRoutes([{ path: '/', element: <Probe /> }], '/')
  await waitFor(() => expect(reads).toBe(1))
  act(() => { stream().emit('message.created', { ...current, text: 'a very long live full text received first' }, '9'); stream().emit('message.created', { ...current, text: 'short text' }, '10') })
  expect(await screen.findByText('short text')).toBeVisible(); expect(screen.queryByText('old HTTP text')).toBeNull()
  const original = document.querySelector('[data-message-id]')
  current = { ...current, text: 'short text' }; await act(async () => { release(); await gate })
  await waitFor(() => expect(env.client.getQueryState(taskKeys.messages(taskId, nodeId))?.fetchStatus).toBe('idle')); expect(reads).toBe(1); expect(screen.getByText('short text')).toBeVisible(); expect(document.querySelector('[data-message-id]')).toBe(original)
  expect(document.querySelectorAll('[data-message-id]')).toHaveLength(1)
  act(() => window.dispatchEvent(new Event('focus'))); await waitFor(() => expect(reads).toBe(2)); expect(screen.getByText('short text')).toBeVisible()
})

it('after an HTTP terminal state a late active full text does not resume generating, and a reconnect snapshot neither treats the messages as complete nor wipes them', async () => {
  const ended = makeTaskMessage(nodeId, 'finished full text')
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), () => HttpResponse.json({ messages: [ended], nextCursor: null })))
  renderRoutes([{ path: '/', element: <Probe /> }], '/')
  await screen.findByText('finished full text')
  act(() => { stream().emit('message.created', { ...ended, status: 'running', text: 'late active text' }, '20'); stream().interrupt(); stream().open() })
  expect(screen.getByText('finished full text')).toBeVisible(); expect(screen.queryByText('late active text')).toBeNull(); expect(screen.queryByText('Generating')).toBeNull()
})

describe('order and scope of the task message projection', () => {
  it('decimal duplicate or out-of-order IDs are de-duplicated and the same ID cannot change run/role; new messages are not de-duplicated by guessing from their text', () => {
    const projection = taskMessageProjection(createTestQueryClient()), running = { ...makeTaskMessage(), status: 'running' as const }
    expect(projection.message(taskId, '9', running)).toBe(true)
    expect(projection.message(taskId, '10', { ...running, text: 'new text' })).toBe(true)
    expect(projection.message(taskId, '9', running)).toBe(false)
    expect(projection.message(taskId, '10', running)).toBe(false)
    expect(projection.message(taskId, '11', { ...running, runId: 'foreign-turn' })).toBe(false)
    expect(projection.message(taskId, '11', { ...running, role: 'user' })).toBe(false)
    expect(projection.message(taskId, '12', { ...running, id: 'another-message', text: 'new text' })).toBe(true)
    expect(projection.messages(taskId, nodeId).map(row => row.text)).toEqual(['new text', 'new text'])
  })
  it('the HTTP request start, not the text length, decides what is newer; a fresh authoritative read can reconcile, and a partial page does not delete old IDs', () => {
    const projection = taskMessageProjection(createTestQueryClient()), message = { ...makeTaskMessage(), status: 'running' as const }
    const before = projection.beginRead(taskId, nodeId)
    projection.message(taskId, '1', { ...message, text: 'shorter' })
    expect(projection.reconcile(before, [{ ...message, text: 'longer old content' }])[0].text).toBe('shorter')
    const fresh = projection.beginRead(taskId, nodeId)
    expect(projection.reconcile(fresh, [{ ...message, text: 'fresh-authority' }])[0].text).toBe('fresh-authority')
    projection.reconcile(projection.beginRead(taskId, nodeId), [])
    expect(projection.messages(taskId, nodeId)).toHaveLength(1)
  })
  it('a new connection generation blocks HTTP from the previous connection, and tasks, nodeRuns and visits do not leak into each other', () => {
    const projection = taskMessageProjection(createTestQueryClient()), running = { ...makeTaskMessage(), status: 'running' as const }
    projection.message(taskId, '1', running)
    const oldRead = projection.beginRead(taskId, nodeId); projection.connection(taskId)
    expect(projection.reconcile(oldRead, [{ ...running, text: 'old connection HTTP' }])[0].text).toBe(running.text)
    projection.message('other-task', '2', { ...running, text: 'other task' })
    projection.message(taskId, '3', { ...running, nodeRunId: oldNodeId, text: 'old visit' })
    expect(projection.messages(taskId, nodeId).map(row => row.text)).toEqual([running.text])
    expect(taskMessageRows([running], projection.messages(taskId, oldNodeId), nodeId)).toEqual([running])
  })
  it('after SSE receives a terminal state, activity with a larger ID cannot revive it, and a terminal full text can still reconcile safely', () => {
    const projection = taskMessageProjection(createTestQueryClient()), terminal = makeTaskMessage()
    projection.message(taskId, '10', terminal)
    expect(projection.message(taskId, '11', { ...terminal, status: 'running' })).toBe(false)
    expect(projection.message(taskId, '12', { ...terminal, text: 'final reconcile' })).toBe(true)
    expect(projection.messages(taskId, nodeId)[0].text).toBe('final reconcile')
  })
})
