import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse, type HttpHandler } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import type { Schemas } from '@/api/client'
import { tasksPath } from '@/api/tasks'
import { apiUrl, defaultProject, makePermissionRequest, projectsUrl } from '@/test/api'
import { FakeEventSource } from '@/test/fake-event-source'
import { agentParty, consoleParty, makeInboxMessage } from '@/test/inbox'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { taskInboxRecord } from '@/test/task-inbox'
import { makeTaskDetail, makeTaskMessage, nodeId, runId, taskId } from '@/test/tasks'

// The current node of a task whose turn waits on requests to approve (a Supervised Agent).
function setup(requests: Schemas['PermissionRequest'][], extra: HttpHandler[] = []) {
  const detail = makeTaskDetail(defaultProject.id)
  const node = detail.nodes[1]
  const reply: Schemas['TaskMessage'] = { ...makeTaskMessage(nodeId, 'Pushing the branch.'), status: 'running' }
  const answers: { id: string; body: Schemas['PermissionAnswer'] }[] = []
  // The approval question the instance also sent the console: in the Inbox, but in the node chat it is the card.
  const question = taskInboxRecord({ message: makeInboxMessage({ from: agentParty({ id: node.agentId, name: node.agentName, address: null }), to: consoleParty, text: 'Approve: git push', question: { kind: 'permission', permissionRequestId: requests[0]?.id ?? crypto.randomUUID(), options: [], expiresAt: '2026-12-01T00:00:00Z', state: 'open', answer: null } }) })
  server.use(
    ...extra,
    ...baseHandlers([]),
    http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: params.node, agentId: node.agentId, runtimeId: node.runtimeId, canSend: true, reason: null, handlingHint: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/messages`), ({ params }) => HttpResponse.json({ messages: params.node === nodeId ? [reply] : [], nextCursor: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/inbox-messages`), ({ params }) => HttpResponse.json({ items: params.node === nodeId ? [question] : [], nextCursor: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/queue`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: params.node, items: [] })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/permission-requests`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: params.node, runtimeId: node.runtimeId, requests: params.node === nodeId ? requests : [] })),
    http.post(apiUrl('/permission-requests/:id/answer'), async ({ params, request }) => {
      const body = (await request.json()) as Schemas['PermissionAnswer']
      answers.push({ id: String(params.id), body })
      return HttpResponse.json({ ...requests.find((r) => r.id === params.id)!, state: 'approved', answeredAt: '2026-10-14T08:01:00Z' })
    }),
  )
  const user = userEvent.setup()
  renderRoutes(routes, `${tasksPath(defaultProject.name)}/${taskId}`)
  const events = () => FakeEventSource.instances.find((source) => source.url.endsWith(`/tasks/${taskId}/events`))!
  return { user, node, answers, events }
}

describe('Approving in a task node', () => {
  it('the node’s reply shows the requests its turn waits on, answered there; the task stream replaces the list', async () => {
    const push = makePermissionRequest({ runId, taskId, nodeRunId: nodeId, title: 'git push origin main' })
    const { user, node, answers, events } = setup([push])
    const pushCard = (await screen.findByText('git push origin main')).closest('[data-permission-request]') as HTMLElement
    expect(within(pushCard).getByRole('heading', { name: `${node.agentName} wants to run a command` })).toBeInTheDocument()
    expect(screen.getByText('1 request is waiting for your approval; the reply waits here')).toBeInTheDocument()
    // The approval question is the card, not a second message.
    expect(screen.queryByText('Approve: git push')).toBeNull()

    await user.click(within(pushCard).getByRole('button', { name: 'Approve' }))
    await waitFor(() => expect(answers).toEqual([{ id: push.id, body: { decision: 'approve' } }]))
    act(() => events().emit('task.node.permission_requests.updated', { taskId, nodeRunId: nodeId, runtimeId: node.runtimeId, requests: [] }, '40'))
    await waitFor(() => expect(screen.queryByText('git push origin main')).toBeNull())
    expect(screen.queryByText(/waiting for your approval/)).toBeNull()
  })

  it('an event that comes while the list is being read wins over the older read', async () => {
    const push = makePermissionRequest({ runId, taskId, nodeRunId: nodeId, title: 'git push origin main' })
    let release = () => {}
    const gate = new Promise<void>((resolve) => (release = resolve))
    let reads = 0
    const slow = http.get(apiUrl(`/tasks/${taskId}/nodes/:node/permission-requests`), async ({ params }) => {
      reads += 1
      await gate
      return HttpResponse.json({ taskId, nodeRunId: params.node, runtimeId: 'old-read', requests: params.node === nodeId ? [push] : [] })
    })
    const { node, events } = setup([push], [slow])
    await waitFor(() => expect(reads).toBe(1))
    await screen.findByText('Pushing the branch.')
    act(() => events().emit('task.node.permission_requests.updated', { taskId, nodeRunId: nodeId, runtimeId: node.runtimeId, requests: [] }, '41'))
    await act(async () => { release(); await new Promise((resolve) => setTimeout(resolve, 50)) })
    expect(screen.queryByText('git push origin main')).toBeNull()
  })
})
