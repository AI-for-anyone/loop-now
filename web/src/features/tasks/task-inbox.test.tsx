import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { taskInboxProjection, useTaskInboxMessages } from '@/api/task-inbox'
import { renderRoutes, createTestQueryClient } from '@/test/render'
import { makeTaskDetail, makeTaskMessage, nodeId, oldNodeId, taskId, runId } from '@/test/tasks'
import { taskInboxRecord } from '@/test/task-inbox'
import { defaultProject, projectsUrl, apiUrl } from '@/test/api'
import { server } from '@/test/server'
import { FakeEventSource } from '@/test/fake-event-source'
import { baseHandlers } from '@/test/shell'
import { routes } from '@/app/routes'
import { tasksPath } from '@/api/tasks'

it('real Inbox record SSE events merge directly, notification, read and reply stay separate, and work output neither poses as an Inbox reply nor is de-duplicated by its text', async () => {
  const detail = makeTaskDetail(defaultProject.id), node = detail.nodes[1], root = taskInboxRecord()
  let records = [root], reads = 0, readPosts = 0
  server.use(...baseHandlers([]), http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: params.node, agentId: node.agentId, runtimeId: node.runtimeId, canSend: false, reason: 'Read-only check', handlingHint: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/messages`), () => HttpResponse.json({ messages: [], nextCursor: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/inbox-messages`), ({ params }) => { reads++; return HttpResponse.json({ items: params.node === nodeId ? records : [], nextCursor: null }) }),
    http.post(apiUrl('/inbox/messages/:id/read'), () => { readPosts++; return HttpResponse.json({}) }))
  renderRoutes(routes, `${tasksPath(defaultProject.name)}/${taskId}`)
  await screen.findByText('真实Inbox输入'); expect(screen.getByText('Received; unread by the Agent')).toBeVisible()
  const es = FakeEventSource.instances.find(source => source.url.endsWith(`/tasks/${taskId}/events`))!
  const notified = { ...root, runLinks: [{ runId, role: 'notification' as const }] }
  records = [notified]; const before = reads
  act(() => es.emit('task.inbox.updated', notified, '10'))
  await waitFor(() => expect(document.querySelector('[data-inbox-message-id] details')).not.toBeNull())
  expect(screen.getByText('Received; unread by the Agent')).toBeVisible(); expect(reads).toBe(before)
  const read = { ...notified, message: { ...root.message, readAt: '2026-10-18T00:01:00Z' }, runLinks: [...notified.runLinks, { runId, role: 'read' as const }] }
  const reply: Schemas['TaskNodeInboxRecord'] = { ...root, message: { ...root.message, id: '52000000-0000-7000-8000-000000000001', inReplyTo: root.message.id, from: root.message.to, to: root.message.from, text: 'same actual text', whenBusy: null, readAt: null }, runLinks: [{ runId, role: 'reply' }] }
  records = [read, reply]
  act(() => { es.emit('task.inbox.updated', read, '11'); es.emit('task.inbox.updated', root, '9'); es.emit('task.inbox.updated', reply, '12'); es.emit('message.created', makeTaskMessage(nodeId, 'same actual text'), '13') })
  await screen.findByText('Read by the Agent'); await waitFor(() => expect(screen.getAllByText('same actual text')).toHaveLength(2))
  expect(screen.getByText('Inbox reply')).toBeVisible(); expect(screen.getByText('Agent work output')).toBeVisible()
  expect(document.querySelectorAll(`[data-inbox-message-id="${root.message.id}"]`)).toHaveLength(1); expect(readPosts).toBe(0)
  expect(within(screen.getByText('真实Inbox输入').closest<HTMLElement>('[data-inbox-message-id]')!).queryByText('Inbox reply')).toBeNull()
})

it('the Inbox read request start protects read, links and not-run facts, and a fresh partial page neither deletes nor downgrades them', () => {
  const store = taskInboxProjection(createTestQueryClient()), root = taskInboxRecord(), stamp = store.begin()
  store.scope(taskId, nodeId, root.agentId, root.runtimeId)
  const newer = { ...root, message: { ...root.message, readAt: '2026-10-18T00:01:00Z' }, runLinks: [{ runId, role: 'read' as const }], notExecuted: { reason: 'node_changed', at: '2026-10-18T00:02:00Z' } }
  expect(store.event(taskId, '10', newer)).toBe(true); store.read(stamp, root)
  expect(store.rows(taskId, nodeId)).toEqual([newer]); expect(store.event(taskId, '10', root)).toBe(false)
  store.read(store.begin(), root)
  expect(store.rows(taskId, nodeId)[0]).toEqual(newer)
  expect(store.event('other-task', '11', root)).toBe(false)
  expect(store.event(taskId, '12', { ...root, runtimeId: 'foreign-runtime' })).toBe(false)
  expect(store.rows(taskId, oldNodeId)).toEqual([])
})

it('each page of the original node has its own cursor, responses with a mismatched scope are filtered out, and loaded IDs are not lost', async () => {
  const root = taskInboxRecord(), node = makeTaskDetail(defaultProject.id).nodes[1]
  function Probe() { const q = useTaskInboxMessages(taskId, nodeId, node.agentId, node.runtimeId); return <><p>{q.data?.items.length}</p><button onClick={() => void q.fetchNextPage()}>more</button></> }
  const cursors: (string | null)[] = []
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/inbox-messages`), ({ request }) => { const cursor = new URL(request.url).searchParams.get('cursor'); cursors.push(cursor); return HttpResponse.json({ items: cursor ? [{ ...root, message: { ...root.message, id: 'second-id' } }] : [root], nextCursor: cursor ? null : 'node-only-cursor' }) }))
  const { router } = renderRoutes([{ path: '/', element: <Probe /> }], '/'); expect(router.state.location.pathname).toBe('/')
  await screen.findByText('1'); await act(async () => { screen.getByText('more').click() }); await screen.findByText('2'); expect(cursors).toEqual([null, 'node-only-cursor'])
})

it('image references survive late HTTP while task SSE stays on the same original message', () => {
  const store = taskInboxProjection(createTestQueryClient()), root = taskInboxRecord(), started = store.begin()
  const images: Schemas['MessageImage'][] = [{ id: '00000000-0000-7000-8000-000000000044', mimeType: 'image/png', size: 32, width: 2, height: 2 }]
  const next = { ...root, message: { ...root.message, images } }
  store.scope(taskId, nodeId, root.agentId, root.runtimeId)
  expect(store.event(taskId, '10', next)).toBe(true)
  store.read(started, root)
  expect(store.rows(taskId, nodeId)[0].message.images).toEqual(images)
  store.read(store.begin(), root)
  expect(store.rows(taskId, nodeId)[0].message.images).toEqual(images)
  expect(store.rows(taskId, nodeId)).toHaveLength(1)
})
