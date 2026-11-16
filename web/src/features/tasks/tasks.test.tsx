import { taskInboxReceipt } from '@/test/task-inbox'
import { t } from '@/i18n'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { renderRoutes, renderHookWithQuery } from '@/test/render'
import { agentsUrl, apiUrl, defaultProject, errorBody, makeAgent, projectsUrl, toListItem, unknownEndpointBody } from '@/test/api'
import { baseHandlers } from '@/test/shell'
import { server } from '@/test/server'
import { FakeEventSource } from '@/test/fake-event-source'
import { makeTaskDetail, makeTaskMessage, nodeId, oldNodeId, taskId } from '@/test/tasks'
import { taskKeys, tasksPath, useTaskMessages } from '@/api/tasks'
import { lastScope } from '@/lib/scope'
import type { Schemas } from '@/api/client'
import { acceptTaskEvent } from './useTaskLive'
import { narrowQuery } from '@/lib/use-media-query'

const inboxCopy = t.taskInbox

function setup(initial?: Schemas['TaskDetail']) {
  let detail = initial ?? makeTaskDetail(defaultProject.id, defaultProject.name)
  let message = makeTaskMessage()
  const calls: { node: string; cursor: string | null }[] = []
  server.use(...baseHandlers([]), http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks: [detail.task], nextCursor: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: String(params.node), agentId: detail.nodes[1].agentId, runtimeId: detail.nodes[1].runtimeId, canSend: false, reason: 'The task instance is unavailable right now', handlingHint: 'Check the current state' })),
    http.get(apiUrl(`/tasks/${taskId}`), () => HttpResponse.json(detail)),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/messages`), ({ params, request }) => {
      const node = String(params.node); calls.push({ node, cursor: new URL(request.url).searchParams.get('cursor') })
      return HttpResponse.json({ messages: [node === nodeId ? message : makeTaskMessage(node, 'history node reply')], nextCursor: null })
    }))
  const emit = (type: string, data: unknown, id = '1') => {
    const es = FakeEventSource.instances.find((s) => s.url.endsWith(`/tasks/${taskId}/events`))!
    act(() => es.emit(type, data, id))
  }
  const open = () => act(() => FakeEventSource.instances.forEach((es) => es.open()))
  return { get detail() { return detail }, set detail(next) { detail = next }, get message() { return message }, set message(next) { message = next }, calls, emit, open }
}
const detailPath = () => `${tasksPath(defaultProject.name)}/${taskId}`
async function renderDetail() {
  const env = setup()
  const result = renderRoutes(routes, detailPath())
  await screen.findByRole('heading', { name: 'General task acceptance' })
  env.open()
  await screen.findByText('Reply from the task instance')
  return { ...env, env, ...result, user: userEvent.setup() }
}

describe('project tasks', () => {
  it('the list shows only project facts, a responsible Agent does not mean running, and Tasks is the current item in the sidebar', async () => {
    const env = setup(); renderRoutes(routes, tasksPath(defaultProject.name)); env.open()
    expect(await screen.findByRole('link', { name: 'General task acceptance' })).toHaveAttribute('href', detailPath())
    const nav = screen.getByRole('navigation', { name: 'Main navigation' })
    expect(within(nav).getAllByRole('link').filter((l) => l.getAttribute('aria-current') === 'page').map((l) => l.textContent)).toEqual(['Tasks'])
    // Since the redesign, “Blocked” also appears in the status label and the stub, so only look at this row in the list.
    expect(within(screen.getByRole('list', { name: 'Tasks' })).getByText('Blocked')).toBeVisible()
  })
  it('the platform scope asks for a project first and does not preselect the default project', async () => {
    setup(); lastScope.set('platform'); const { router } = renderRoutes(routes, '/tasks')
    expect(await screen.findByRole('dialog')).toHaveTextContent('Choose a project to see its tasks')
    expect(router.state.location.pathname).toBe('/tasks')
    await userEvent.click(screen.getByRole('link', { name: defaultProject.name }))
    await waitFor(() => expect(router.state.location.pathname).toBe(tasksPath(defaultProject.name)))
  })
  it('the default tasks entry uses the most recent project', async () => {
    setup(); lastScope.set(defaultProject.id); const { router } = renderRoutes(routes, '/tasks')
    await screen.findByRole('link', { name: 'General task acceptance' })
    expect(router.state.location.pathname).toBe(tasksPath(defaultProject.name))
  })
  it('task detail header: a hollow “#12” before the title (not part of the title), the breadcrumb project›Tasks›#12, the status stamp and archive state beside it, and a red-outlined “Cancel task” on the right', async () => {
    setup(); renderRoutes(routes, detailPath())
    const heading = await screen.findByRole('heading', { level: 1, name: 'General task acceptance' })
    const number = heading.previousElementSibling!
    expect(number).toHaveTextContent(/^#12$/)
    expect(number).toHaveAttribute('aria-hidden', 'true')
    expect(number).toHaveClass('font-display-latin', 'text-transparent')
    const header = heading.closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(`${defaultProject.name}›Tasks›#12`)
    expect(within(header).getByRole('link', { name: 'Tasks' })).toHaveAttribute('href', tasksPath(defaultProject.name))
    expect(within(header).getByText('Blocked')).toHaveClass('riso-tilt', 'text-red-text')
    expect(within(header).getByText('Not archived yet')).toBeInTheDocument()
    expect(within(header).getByRole('button', { name: 'Cancel task' })).toHaveClass('border-red-text')
  })
  it('a task of another project does not show its details', async () => {
    const d = makeTaskDetail('other-project'); setup(d); renderRoutes(routes, detailPath())
    expect(await screen.findByRole('alert')).toHaveTextContent("This task doesn't belong to the current project")
    expect(screen.queryByRole('button', { name: 'Retry current node' })).not.toBeInTheDocument()
  })
  it('an empty list has no create-task action', async () => {
    setup(); server.use(http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks: [], nextCursor: null })))
    renderRoutes(routes, tasksPath(defaultProject.name)); expect(await screen.findByText(/No tasks yet/)).toBeVisible()
    expect(screen.queryByRole('button', { name: /New task/ })).not.toBeInTheDocument()
  })
})

describe('node records and updates', () => {
  it('initial record and graph navigation preserve the original task action locator', async () => {
    setup()
    const key = `taskLarkSwitch.${taskId}`, original = '30000000-0000-7000-8000-000000000001'
    const { router } = renderRoutes(routes, `${detailPath()}?${key}=${original}`)
    await screen.findByText('Reply from the task instance')
    await waitFor(() => expect(new URLSearchParams(router.state.location.search).get('record')).toBe(nodeId))
    expect(new URLSearchParams(router.state.location.search).get(key)).toBe(original)
    await userEvent.click(screen.getByRole('button', { name: /Submit report/ }))
    await waitFor(() => expect(new URLSearchParams(router.state.location.search).get('step')).toBe('finish'))
    expect(new URLSearchParams(router.state.location.search).get(key)).toBe(original)
    expect(new URLSearchParams(router.state.location.search).has('record')).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: /Quality check/ }))
    await waitFor(() => expect(new URLSearchParams(router.state.location.search).get('record')).toBe(nodeId))
    expect(new URLSearchParams(router.state.location.search).get(key)).toBe(original)
  })
  it('consecutive conversations of the same step, grouped materials and the old record anchor are kept and not switched by SSE', async () => {
    const { user, router, env } = await renderDetail()
    expect(screen.getByRole('tab', { name: 'Conversation' })).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByText('history node reply')).toBeVisible()
    expect(screen.getByText('Reply from the task instance')).toBeVisible()
    await user.click(screen.getByRole('button', { name: /Run boundary · Visit 1/ }))
    expect(screen.getByRole('tab', { name: 'Node materials' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByText(/Earlier successful result/)).toBeVisible()
    expect(screen.getByText(/for inspection only and is not passed downstream as a success/)).toBeVisible()
    await user.click(screen.getByRole('link', { name: /01 Quality check · output of visit 1/ }))
    expect(router.state.location.search).toContain(oldNodeId)
    expect(screen.getByText('Reply from the task instance')).toBeVisible()
    expect(screen.getByRole('textbox', { name: 'Message draft' })).toHaveAttribute('readonly')
    expect(screen.queryByRole('button', { name: 'Retry current node' })).not.toBeInTheDocument()
    env.detail = { ...env.detail, task: { ...env.detail.task, status: 'running', version: 3 } }
    env.emit('task.updated', { task: env.detail.task }, '2')
    await waitFor(() => expect(screen.getByText('View current node')).toBeVisible())
    expect(router.state.location.search).toContain(oldNodeId)
    expect(env.calls.every(c => c.node === nodeId || c.node === oldNodeId)).toBe(true)
  })
  it('the node conversation scroll area is the positioning container, so absolutely positioned text inside it does not stretch the whole page', async () => {
    await renderDetail()
    expect(document.querySelector('[data-conversation-scroll]')).toHaveClass('relative')
  })
  it('a full-text update with the same message ID does not duplicate the bubble, and decimal event IDs are not compared in dictionary order', async () => {
    const { env } = await renderDetail()
    env.message = { ...env.message, text: 'final text after the update' }
    env.emit('message.created', env.message, '10')
    expect(await screen.findByText('final text after the update')).toBeVisible()
    env.emit('message.created', env.message, '10')
    expect(screen.getAllByText('final text after the update')).toHaveLength(1)
    expect(acceptTaskEvent('9', '10')).toBe(true); expect(acceptTaskEvent('10', '9')).toBe(false)
  })
  it('a closed question cannot be answered, artifact downloads use the platform path, and the archive state shows on its own', async () => {
    const d = makeTaskDetail(defaultProject.id); d.cleanup = 'archived'; d.task.status = 'cancelled'; d.nodes[1].question = { id: nodeId, threadId: taskId, messageId: oldNodeId, text: 'Confirm the result?', status: 'closed', canAnswer: false }
    setup(d); renderRoutes(routes, detailPath()); await screen.findByRole('heading', { name: 'General task acceptance' }); await userEvent.click(screen.getByText('Open question · Closed')); expect(screen.getByText('Confirm the result?')).toBeVisible()
    expect(screen.queryByRole('link', { name: 'Go answer' })).not.toBeInTheDocument()
    expect(screen.getByText('Records archived; copies reclaimed')).toBeVisible()
    await userEvent.click(screen.getByRole('tab', { name: 'Node materials' })); await userEvent.click(screen.getByText('Visit 1 · attempt 1')); expect(screen.getByRole('link', { name: 'Download' })).toHaveAttribute('href', apiUrl(d.artifacts[0].downloadPath))
  })
  it('an invalid selected record is stated plainly instead of silently switching to another one', async () => {
    setup(); renderRoutes(routes, detailPath() + '?record=missing')
    expect(await screen.findByText('Can\'t find that record. Pick the node again.')).toBeVisible()
    expect(screen.queryByText('Reply from the task instance')).not.toBeInTheDocument()
  })
  it('losing the connection disables control actions, and a focus refresh fills in the facts', async () => {
    const { env, client } = await renderDetail()
    const es = FakeEventSource.instances.find((s) => s.url.endsWith(`/tasks/${taskId}/events`))!
    act(() => es.interrupt()); expect(screen.getByRole('button', { name: 'Retry current node' })).toBeDisabled()
    env.detail = { ...env.detail, cleanup: 'pending' }
    act(() => window.dispatchEvent(new Event('focus')))
    await waitFor(() => expect(client.getQueryData<Schemas['TaskDetail']>(taskKeys.detail(taskId))?.cleanup).toBe('pending'))
  })
})

describe('control actions', () => {
  it('retry keeps the note, a stale target is not redirected to the new node, and write actions are not retried automatically', async () => {
    const { env, user } = await renderDetail(); const bodies: Schemas['TaskRetryInput'][] = []
    server.use(http.post(apiUrl(`/tasks/${taskId}/retry`), async ({ request }) => { bodies.push(await request.json() as Schemas['TaskRetryInput']); return HttpResponse.json(errorBody('task_conflict', 'The old record has changed'), { status: 409 }) }))
    await user.click(screen.getByRole('button', { name: 'Retry current node' })); const dialog = screen.getByRole('alertdialog')
    await user.type(within(dialog).getByRole('textbox'), 'keep this note')
    env.detail = { ...env.detail, task: { ...env.detail.task, currentNodeRunId: oldNodeId, version: 3 } }
    env.emit('task.updated', { task: env.detail.task }, '3')
    await user.click(within(dialog).getByRole('button', { name: 'Retry current node' }))
    await within(dialog).findByText(/The old record has changed/)
    expect(within(dialog).getByRole('textbox')).toHaveValue('keep this note')
    expect(bodies).toHaveLength(1); expect(bodies[0].expectedNodeRunId).toBe(nodeId); expect(bodies[0].note).toBe('keep this note')
  })
  it('after cancelling, archiving still shows as in progress, and accepted is not passed off as cleanup done', async () => {
    const { env, user } = await renderDetail(); let calls = 0
    server.use(http.post(apiUrl(`/tasks/${taskId}/cancel`), async ({ request }) => { expect(await request.json()).toHaveProperty('actionId'); calls++; env.detail = { ...env.detail, task: { ...env.detail.task, status: 'cancelled', version: 3 }, cleanup: 'pending' }; return HttpResponse.json(env.detail) }))
    await user.click(screen.getByRole('button', { name: 'Cancel task' })); await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Cancel task' }))
    expect(await screen.findByText('Archiving and reclaiming')).toBeVisible(); expect(calls).toBe(1)
    expect(screen.queryByRole('button', { name: 'Retry current node' })).not.toBeInTheDocument()
  })
})

it('task conversations are isolated by task and nodeRun, paging continues by cursor, and only the latest value of an ID is kept', async () => {
  const env = setup()
  const otherTask = '00000000-0000-7000-8000-000000000555'
  server.use(http.get(apiUrl(`/tasks/${otherTask}/nodes/${nodeId}/messages`), () => HttpResponse.json({ messages: [makeTaskMessage(nodeId, 'message of another task')], nextCursor: null })))
  const { result } = renderHookWithQuery(() => ({ first: useTaskMessages(taskId, nodeId), second: useTaskMessages(otherTask, nodeId) }))
  await waitFor(() => expect(result.current.second.data?.pages[0].messages[0].text).toBe('message of another task'))
  expect(result.current.first.data?.pages[0].messages[0].text).toBe(env.message.text)
})
it('paged node messages update by the same ID, and messages of other nodes are not mixed into the current record', async () => {
  const env = setup()
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), ({ request }) => {
    const next = new URL(request.url).searchParams.has('cursor')
    return HttpResponse.json(next ? { messages: [{ ...env.message, text: 'update after paging' }, makeTaskMessage(oldNodeId, 'not from the current node')], nextCursor: null } : { messages: [env.message], nextCursor: 'page-2' })
  }))
  renderRoutes(routes, detailPath()); await screen.findByText('Reply from the task instance')
  await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
  expect(await screen.findByText('update after paging')).toBeVisible()
  expect(screen.queryByText('Reply from the task instance')).not.toBeInTheDocument()
  expect(screen.queryByText('not from the current node')).not.toBeInTheDocument()
})
it('project task events discover new tasks, and the list is fetched again after reconnecting', async () => {
  const env = setup(); let summaries = [env.detail.task]
  server.use(http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks: summaries, nextCursor: null })))
  renderRoutes(routes, tasksPath(defaultProject.name)); await screen.findByRole('link', { name: 'General task acceptance' })
  const stream = FakeEventSource.instances.find((s) => s.url.endsWith(`/projects/${defaultProject.id}/tasks/events`))!
  act(() => stream.open()); summaries = [...summaries, { ...env.detail.task, id: oldNodeId, title: 'Task created by the Manager' }]
  act(() => stream.emit('task.updated', { task: summaries[1] }, '9'))
  expect(await screen.findByRole('link', { name: 'Task created by the Manager' })).toBeVisible()
  summaries = [{ ...env.detail.task, title: 'Task updated while disconnected' }]
  act(() => { stream.interrupt(); stream.open() })
  expect(await screen.findByRole('link', { name: 'Task updated while disconnected' })).toBeVisible()
})
it('a project rename is followed by its stable ID, keeping deep links to the task and node record', async () => {
  const { client, router } = await renderDetail()
  server.use(http.get(projectsUrl, () => HttpResponse.json({ projects: [{ ...defaultProject, name: 'Renamed task project' }] })))
  await act(() => client.invalidateQueries({ queryKey: ['projects'] }))
  await waitFor(() => expect(decodeURIComponent(router.state.location.pathname)).toContain('/p/Renamed task project/tasks/'))
  expect(router.state.location.search).toContain(nodeId)
})
it('steps on the workflow ruler can be selected with the keyboard; the canvas drag, zoom and locate controls are gone', async () => {
  const detail = makeTaskDetail(defaultProject.id)
  detail.workflow.steps.push({ ...detail.workflow.steps[0], id: 'revise', title: 'Rework' })
  detail.workflow.edges.push({ id: 'branch', from: 'check', to: 'revise', label: 'needs revision' }, { id: 'return', from: 'revise', to: 'check', label: 'check again', maxTraversals: 2 })
  setup(detail); const { router } = renderRoutes(routes, detailPath())
  const ruler = await screen.findByRole('navigation', { name: 'Workflow' })
  const button = within(ruler).getByRole('button', { name: /Rework/ })
  button.focus(); await userEvent.keyboard('{Enter}')
  await waitFor(() => expect(router.state.location.search).toContain('step=revise'))
  // The new URL reaches the page only inside a transition; wait for the page to catch up.
  expect(await screen.findByText('This node hasn\'t been visited yet.')).toBeVisible()
  // A step that has not been visited has no chat panel: the sheet shows only a one-line
  // note, and the stub still holds the workflow ruler (as a different element).
  const after = screen.getByRole('navigation', { name: 'Workflow' })
  expect(within(after).getByRole('button', { name: /Rework/ })).toHaveAttribute('aria-pressed', 'true')
  for (const name of ['Fit view', 'Locate current', 'Zoom in', 'Zoom out']) expect(screen.queryByRole('button', { name })).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Workflow canvas; drag or use the arrow keys to pan')).not.toBeInTheDocument()
  expect(within(after).getAllByRole('button')).toHaveLength(3)
})
it('on narrow screens (stub hidden), a row of small step tags sits above the index tabs, and a click selects that step too', async () => {
  const original = window.matchMedia
  window.matchMedia = ((query: string) => ({ matches: query === narrowQuery, media: query, onchange: null, addListener: () => {}, removeListener: () => {}, addEventListener: () => {}, removeEventListener: () => {}, dispatchEvent: () => false })) as typeof window.matchMedia
  try {
    setup(); const { router } = renderRoutes(routes, detailPath())
    await screen.findByRole('heading', { name: 'General task acceptance' })
    const [chips, ruler] = screen.getAllByRole('navigation', { name: 'Workflow' })
    expect(chips).toHaveAttribute('data-workflow-chips')
    expect(chips.compareDocumentPosition(screen.getByRole('tablist', { name: 'Node record' })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(ruler).toHaveClass('max-nav:hidden')
    await userEvent.click(within(chips).getByRole('button', { name: /Submit report/ }))
    await waitFor(() => expect(router.state.location.search).toContain('step=finish'))
  } finally {
    window.matchMedia = original
  }
})
it('node materials: the visit being viewed is an expanded ticket and the others are folded; inputs from other visits are links, artifacts are file cards with a download button, and you can go back to this conversation', async () => {
  const { user } = await renderDetail()
  await user.click(screen.getByRole('tab', { name: 'Node materials' }))
  const viewing = document.querySelector(`[data-material-record="${nodeId}"]`) as HTMLDetailsElement
  const older = document.querySelector(`[data-material-record="${oldNodeId}"]`) as HTMLDetailsElement
  expect(viewing.open).toBe(true)
  expect(older.open).toBe(false)
  expect(viewing.compareDocumentPosition(older) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(within(viewing).getByText('V2·A1')).toHaveClass('bg-ink')
  // The yellow highlighter marks on the three subheadings Input, Output and Artifacts cover only the text.
  for (const name of ['Inputs', 'Output', 'Artifacts']) expect(within(viewing).getByRole('heading', { name })).toHaveClass('riso-marker', 'inline-block')
  expect(within(viewing).getByRole('note')).toHaveTextContent('for inspection only and is not passed downstream as a success')
  expect(within(viewing).getByRole('link', { name: '01 Quality check · output of visit 1' })).toHaveAttribute('href', expect.stringContaining(`record=${oldNodeId}`))
  await user.click(within(older).getByText('Visit 1 · attempt 1'))
  const download = within(older).getByRole('link', { name: 'Download' })
  expect(download).toHaveAccessibleDescription('saved-report.txt')
  expect(download.closest('li')).toHaveClass('border-2', 'border-ink')
  await user.click(within(viewing).getByRole('button', { name: 'Back to this conversation' }))
  expect(screen.getByRole('tab', { name: 'Conversation' })).toHaveAttribute('aria-selected', 'true')
})
it('when the same Agent runs another node, the message scope switches instead of reusing the previous node conversation', async () => {
  const env = setup()
  const otherId = '00000000-0000-7000-8000-000000000401'
  const otherRun = '00000000-0000-7000-8000-000000000402'
  env.detail = { ...env.detail, nodes: [...env.detail.nodes, { ...env.detail.nodes[1], id: otherId, stepId: 'finish', status: 'completed', visit: 1, inputs: { from: 'input of another node' }, output: { report: 'result of another node' }, runIds: [otherRun] }] }
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${otherId}/messages`), () => HttpResponse.json({ messages: [{ ...makeTaskMessage(otherId, 'another node conversation of the same Agent'), runId: otherRun }], nextCursor: null })))
  renderRoutes(routes, detailPath()); await screen.findByText('Reply from the task instance')
  await userEvent.click(screen.getByRole('button', { name: /Submit report/ }))
  expect(await screen.findByText('another node conversation of the same Agent')).toBeVisible()
  expect(screen.queryByText('Reply from the task instance')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('tab', { name: 'Node materials' })); expect(screen.getByText(/input of another node/)).toBeVisible()
})

it('a failed task turn shows the copy for its error code; the server sentence only restates it', async () => {
  const env = setup(); env.message = { ...env.message, status: 'failed', errorCode: 'pi_error', errorMessage: 'Pi returned an error.' }
  renderRoutes(routes, detailPath())
  expect(await screen.findByText('Agent reported an error')).toBeVisible()
  expect(screen.queryByText(/Pi returned an error/)).toBeNull()
})

// The missing endpoint is recognized by the server's signal (details.reason unknown_endpoint) and, for older servers, by their fixed
// text (that case is in src/zh-CN/features/tasks/tasks.zh.test.tsx); once recognized, task events no longer re-read the capability
// (gets stays 1).
it.each([
  [404, 'not_found', 'No such endpoint: GET /api/v1/tasks/…/message-capability', true, { reason: 'unknown_endpoint' }],
  [404, 'not_found', 'Node run not found', false, undefined],
  [401, 'unauthenticated', 'A console identity is required', false, undefined],
  [403, 'forbidden', 'No permission to act on this node', false, undefined],
  [500, 'internal_error', 'Cannot check the target right now', false, undefined],
] as const)('capability failure %s/%s disables sending safely and keeps the draft; only a missing endpoint means an old server', async (status, code, message, unsupported, details) => {
  const env = setup(); let gets = 0
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), () => { gets++; return HttpResponse.json(errorBody(code, message, details ? { details: details as never } : {}), { status }) }))
  renderRoutes(routes, detailPath()); await screen.findByText('Reply from the task instance')
  const box = screen.getByRole('textbox', { name: 'Message draft' })
  await userEvent.type(box, 'original node draft')
  expect(await screen.findByText(unsupported ? 'Node messages aren\'t enabled on this server yet' : new RegExp(message))).toBeVisible()
  expect(screen.getByRole('button', { name: 'Send' })).toBeDisabled()
  env.emit('node.updated', { node: env.detail.nodes[1] }, '20')
  await waitFor(() => expect(box).toHaveValue('original node draft'))
  if (unsupported) { await new Promise(resolve => setTimeout(resolve, 120)); expect(gets).toBe(1) }
  expect(screen.queryByText(unsupported ? `Not found, or already deleted: ${message}` : 'Node messages aren\'t enabled on this server yet')).not.toBeInTheDocument()
})

it('a new attempt or the end of the task keeps the original nodeRun draft; a new empty draft appears only when you pick the new run yourself', async () => {
  const { env, user, router } = await renderDetail()
  await user.type(screen.getByRole('textbox', { name: 'Message draft' }), 'draft only for the old run')
  const next = { ...env.detail.nodes[1], id: '00000000-0000-7000-8000-000000000901', attempt: 2 }
  env.detail = { ...env.detail, nodes: [...env.detail.nodes, next], task: { ...env.detail.task, currentNodeRunId: next.id, version: 4 } }
  env.emit('node.updated', { node: next }, '21')
  await waitFor(() => expect(screen.getByRole('textbox', { name: 'Message draft' })).toHaveAttribute('readonly'))
  expect(screen.getByRole('textbox', { name: 'Message draft' })).toHaveValue('draft only for the old run')
  expect(router.state.location.search).toContain(nodeId)
  await user.click(screen.getByRole('button', { name: 'View current node' }))
  expect(screen.getByRole('textbox', { name: 'Message draft' })).toHaveValue('')
  expect(screen.getByText('draft only for the old run')).toBeInTheDocument()
  await user.type(screen.getByRole('textbox', { name: 'Message draft' }), 'own draft of the new run')
  env.detail = { ...env.detail, task: { ...env.detail.task, status: 'completed', version: 5 } }
  env.emit('task.updated', { task: env.detail.task }, '22')
  await waitFor(() => expect(screen.getByRole('textbox', { name: 'Message draft' })).toHaveAttribute('readonly'))
  expect(screen.getByRole('textbox', { name: 'Message draft' })).toHaveValue('own draft of the new run')
})

it('during frequent SSE events dirty reconciles run one at a time without starving by cancelling, and after reconciling an old response does not overwrite the new full text', async () => {
  const env = setup(); let count = 0, pending = 0, max = 0
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), async () => {
    count++; pending++; max = Math.max(max, pending)
    const current = count
    if (current === 2) await gate
    pending--
    return HttpResponse.json({ messages: [makeTaskMessage(nodeId, current < 3 ? 'earlier full text' : 'reconciled new full text')], nextCursor: null })
  }))
  renderRoutes(routes, detailPath()); await screen.findByText('earlier full text')
  env.emit('message.created', {}, '1')
  await waitFor(() => expect(count).toBe(2))
  for (let i = 2; i <= 25; i++) env.emit('message.created', {}, String(i))
  release()
  expect(await screen.findByText('reconciled new full text')).toBeVisible()
  expect(max).toBe(1); expect(count).toBe(3)
  expect(screen.queryByText('earlier full text')).not.toBeInTheDocument()
})

it('reconnecting reconciles all loaded pages from the first cursor, and a partial page does not delete messages already seen', async () => {
  const env = setup(); let refreshed = false; const cursors: (string | null)[] = []
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), ({ request }) => {
    const cursor = new URL(request.url).searchParams.get('cursor'); cursors.push(cursor)
    return HttpResponse.json(cursor ? { messages: [makeTaskMessage(nodeId, refreshed ? 'old page error reconciled' : 'old page original content', 'older-page-id')], nextCursor: null } : { messages: refreshed ? [] : [env.message], nextCursor: 'independent-cursor' })
  }))
  renderRoutes(routes, detailPath()); await screen.findByText('Reply from the task instance')
  await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
  await screen.findByText('old page original content')
  refreshed = true; cursors.length = 0
  act(() => { const es = FakeEventSource.instances.find(s => s.url.endsWith(`/tasks/${taskId}/events`))!; es.interrupt(); es.open() })
  expect(await screen.findByText('old page error reconciled')).toBeVisible()
  expect(cursors).toEqual([null, 'independent-cursor'])
  expect(screen.getByText('Reply from the task instance')).toBeVisible()
})

it('two run segments each load more than 50 messages without sharing cursors or gaps, and clicking a node goes back to the conversation by default', async () => {
  setup(); const cursors: { node: string; cursor: string | null }[] = []
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/:node/messages`), ({ params, request }) => {
    const node = String(params.node), cursor = new URL(request.url).searchParams.get('cursor'); cursors.push({ node, cursor })
    return HttpResponse.json({ messages: Array.from({ length: cursor ? 2 : 50 }, (_, index) => makeTaskMessage(node, `${node === nodeId ? 'current segment' : 'history segment'} #${cursor ? 50 + index : index}`, `${node}-${cursor ?? 'first'}-${index}`)), nextCursor: cursor ? null : `cursor-for-${node}` })
  }))
  renderRoutes(routes, detailPath()); await screen.findByText('current segment #49'); await screen.findByText('history segment #49')
  const gaps = screen.getAllByText('Some messages from this run haven\'t loaded yet.'); expect(gaps).toHaveLength(2)
  await userEvent.click(screen.getAllByRole('button', { name: 'Load more' })[0])
  await screen.findByText('history segment #51')
  expect(cursors).toContainEqual({ node: oldNodeId, cursor: `cursor-for-${oldNodeId}` })
  expect(cursors).not.toContainEqual({ node: nodeId, cursor: `cursor-for-${oldNodeId}` })
  await userEvent.click(screen.getByRole('button', { name: 'Load more' }))
  await screen.findByText('current segment #51')
  expect(cursors).toContainEqual({ node: nodeId, cursor: `cursor-for-${nodeId}` })
  await userEvent.click(screen.getByRole('tab', { name: 'Node materials' }))
  await userEvent.click(screen.getByRole('button', { name: /Quality check/ }))
  expect(screen.getByRole('tab', { name: 'Conversation' })).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByText('history segment #51')).toBeVisible()
})

it('a source link across steps locates the original record while keeping all segments of that step, and the latest failed group is expanded by default', async () => {
  const env = setup(), thirdId = '00000000-0000-7000-8000-000000000903'
  const third = { ...env.detail.nodes[1], id: thirdId, stepId: 'finish', visit: 1, attempt: 1, inputVersions: [{ input: 'review', nodeRunId: oldNodeId }] }
  env.detail = { ...env.detail, nodes: [...env.detail.nodes, third], task: { ...env.detail.task, currentStepId: 'finish', currentNodeRunId: thirdId } }
  const { router } = renderRoutes(routes, detailPath())
  await screen.findByRole('heading', { name: 'General task acceptance' })
  await userEvent.click(screen.getByRole('tab', { name: 'Node materials' }))
  expect(screen.getByText(/for inspection only and is not passed downstream as a success/)).toBeVisible()
  await userEvent.click(screen.getByRole('link', { name: /01 Quality check · output of visit 1/ }))
  await waitFor(() => expect(router.state.location.search).toContain(`record=${oldNodeId}`))
  expect(router.state.location.search).toContain('step=check')
  expect(await screen.findByText('Reply from the task instance')).toBeVisible()
  expect(screen.getByText('history node reply')).toBeVisible()
  expect(screen.getByRole('textbox', { name: 'Message draft' })).toHaveAttribute('readonly')
  await userEvent.click(screen.getByRole('tab', { name: 'Node materials' }))
  expect(screen.getByText(/Earlier successful result/)).toBeVisible()
})

it('the waiting state keeps the platform facts, the fixed reason and the original question permission, and shows no retry for history', async () => {
  const detail = makeTaskDetail(defaultProject.id)
  detail.task.status = 'waiting'; detail.nodes[1].status = 'waiting'
  detail.nodes[1].reason = 'Waiting for the Manager to confirm the original question'
  detail.nodes[1].question = { id: oldNodeId, messageId: oldNodeId, threadId: taskId, text: 'Original question from the Manager', status: 'open', canAnswer: false }
  setup(detail); renderRoutes(routes, detailPath())
  await screen.findByRole('heading', { name: 'General task acceptance' })
  const status = document.querySelector('[data-node-status]') as HTMLElement
  expect(within(status).getByText('Waiting for reply')).toBeVisible()
  expect(within(status).getAllByText('Waiting for the Manager to confirm the original question')[0]).toBeVisible()
  await userEvent.click(within(status).getByText('Open question · Open'))
  expect(within(status).getByText('Your current identity can\'t answer this question.')).toBeVisible()
  expect(screen.queryByRole('link', { name: 'Go answer' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Retry current node' })).not.toBeInTheDocument()
  const tab = screen.getByRole('tab', { name: 'Conversation' }); tab.focus(); await userEvent.keyboard('{ArrowRight}')
  expect(screen.getByRole('tab', { name: 'Node materials' })).toHaveFocus()
})

it('after an old backend lacks the capability, window focus fetches the same formal GET again and can recover without changing the draft or target', async () => {
  setup(); let ready = false, count = 0
  const detail = makeTaskDetail(defaultProject.id)
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => { count++; return ready ? HttpResponse.json({ taskId, nodeRunId: String(params.node), agentId: detail.nodes[1].agentId, runtimeId: detail.nodes[1].runtimeId, canSend: false, reason: 'The original session is unavailable', handlingHint: 'Check the task instance' }) : HttpResponse.json(unknownEndpointBody(), { status: 404 }) }))
  renderRoutes(routes, detailPath()); await screen.findByText('Node messages aren\'t enabled on this server yet')
  await userEvent.type(screen.getByRole('textbox', { name: 'Message draft' }), 'draft from before the upgrade')
  ready = true; act(() => window.dispatchEvent(new Event('focus')))
  expect(await screen.findByText('The original session is unavailable · Check the task instance')).toBeVisible()
  expect(screen.queryByText('Node messages aren\'t enabled on this server yet')).not.toBeInTheDocument()
  expect(screen.getByRole('textbox', { name: 'Message draft' })).toHaveValue('draft from before the upgrade')
  expect(count).toBe(2)
})

it('an unknown send result keeps the original action while other nodes are viewed, and confirming it by hand back on the original run creates no new action', async () => {
  const env = setup(); const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: String(params.node), agentId: env.detail.nodes[1].agentId, runtimeId: env.detail.nodes[1].runtimeId, canSend: true, reason: null, handlingHint: null })),
    http.post(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/inbox-messages`), async ({ request }) => {
      const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body)
      return bodies.length === 1 ? HttpResponse.json(errorBody('internal_error', 'The receipt cannot be confirmed'), { status: 500 }) : HttpResponse.json(taskInboxReceipt(body), { status: 201 })
    }))
  renderRoutes(routes, detailPath()); await screen.findByText('Reply from the task instance'); env.open()
  await waitFor(() => expect(screen.getByRole('button', { name: 'Send' })).toBeEnabled())
  await userEvent.type(screen.getByRole('textbox', { name: 'Message draft' }), 'original action draft')
  await userEvent.click(screen.getByRole('button', { name: 'Send' })); await screen.findByRole('button', { name: 'Retry checking the original message' })
  await userEvent.click(screen.getByRole('button', { name: /Submit report/ })); await screen.findByText('This node hasn\'t been visited yet.')
  await userEvent.click(screen.getByRole('button', { name: /Quality check/ }))
  const retry = await screen.findByRole('button', { name: 'Retry checking the original message' })
  expect(screen.getByRole('textbox', { name: 'Message draft' })).toHaveValue('original action draft')
  expect(bodies).toHaveLength(1)
  await waitFor(() => expect(retry).toBeEnabled()); await userEvent.click(retry)
  await screen.findByText(inboxCopy.accepted)
  expect(bodies).toHaveLength(2); expect(bodies[1]).toEqual(bodies[0])
})

it('a plain console_node message shows You, platform init and reminder sources stay as they are, and the console is not passed off as the platform', async () => {
  setup()
  const messages = [
    { ...makeTaskMessage(nodeId, 'plain console message', 'console-message'), role: 'user', origin: 'console_node' },
    { ...makeTaskMessage(nodeId, 'platform init message', 'platform-message'), role: 'user', origin: 'task_node' },
    { ...makeTaskMessage(nodeId, 'Inbox reminder message', 'reminder-message'), role: 'user', origin: 'inbox_reminder' },
  ]
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), () => HttpResponse.json({ messages, nextCursor: null })))
  renderRoutes(routes, detailPath())
  const consoleRow = (await screen.findByText('plain console message')).closest('li')!
  expect(within(consoleRow).getByText('You')).toBeVisible(); expect(within(consoleRow).queryByText('Platform')).not.toBeInTheDocument()
  expect(within(screen.getByText('platform init message').closest('li')!).getByText('Platform')).toBeVisible()
  expect(within(screen.getByText('Inbox reminder message').closest('li')!).getByText(/Platform reminder/)).toBeVisible()
})

// "Printed sheet" redesign, step 3: the task list sits on the sheet, the status
// filters are index tabs along the sheet's top edge, and the progress is in the stub.
describe('task list on the sheet', () => {
  const summaryOf = (detail: Schemas['TaskDetail'], overrides: Partial<Schemas['TaskSummary']>): Schemas['TaskSummary'] => ({ ...detail.task, ...overrides })
  async function renderList(counts = { preparing: 0, running: 1, waiting: 1, blocked: 1 }, nextCursor: string | null = null) {
    const env = setup()
    const base = env.detail
    const tasks = [
      summaryOf(base, { id: '00000000-0000-7000-8000-00000000a001', number: 12, title: 'Sort user feedback for this week', status: 'running', reason: null, currentStepId: 'triage' }),
      summaryOf(base, { id: '00000000-0000-7000-8000-00000000a002', number: 11, title: 'Export timeout fix plan', status: 'blocked', reason: 'Continues once you confirm the timeout limit', currentStepId: 'review' }),
      summaryOf(base, { id: '00000000-0000-7000-8000-00000000a003', number: 10, title: 'Weekly report draft', status: 'completed', reason: null, currentStepId: 'publish' }),
      summaryOf(base, { id: '00000000-0000-7000-8000-00000000a004', number: 9, title: 'Task waiting for a user answer', status: 'waiting', reason: 'Waiting for an answer', currentStepId: null }),
      summaryOf(base, { id: '00000000-0000-7000-8000-00000000a005', number: 8, title: 'Cancelled task', status: 'cancelled', reason: null, currentStepId: null }),
    ]
    server.use(
      http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks, nextCursor })),
      http.get(apiUrl(`/projects/${defaultProject.id}/tasks/counts`), () => HttpResponse.json(counts)),
      http.get(agentsUrl, () => HttpResponse.json({ agents: [toListItem(makeAgent({ id: base.task.currentAgentId!, name: 'task-checker' }))] })),
    )
    renderRoutes(routes, tasksPath(defaultProject.name))
    const list = await screen.findByRole('list', { name: 'Tasks' })
    env.open()
    return { list, user: userEvent.setup() }
  }
  const rowOf = (title: string) => screen.getByRole('link', { name: title }).closest('li')!
  const tabs = () => screen.getByRole('group', { name: 'Status filter' })

  it('the header is the page title: the breadcrumb is the project name and the title is “Tasks”, with the description and live status beside it', async () => {
    await renderList()
    const header = screen.getByRole('heading', { level: 1, name: 'Tasks' }).closest('header')!
    expect(within(header).getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent(`${defaultProject.name}›Tasks`)
    expect(within(header).getByRole('link', { name: defaultProject.name })).toHaveAttribute('href', `/?project=${encodeURIComponent(defaultProject.name)}`)
    expect(within(header).getByText('This project\'s tasks, created by the project Manager')).toBeInTheDocument()
    expect(await within(header).findByText('Live')).toBeInTheDocument()
  })

  it('status filters are index tabs along the top edge of the sheet with counts; the selected one is yellow; they filter only the tasks read so far', async () => {
    const { list, user } = await renderList()
    const buttons = within(tabs()).getAllByRole('button')
    // In progress and Blocked use the server's counts (unfinished tasks); Completed and All count the tasks read so far.
    expect(buttons.map((button) => button.textContent)).toEqual(['All5', 'In progress2', 'Blocked1', 'Completed1'])
    expect(buttons[0]).toHaveAttribute('aria-pressed', 'true')
    expect(buttons[0]).toHaveClass('bg-riso-yellow', 'h-10')
    expect(buttons[1]).toHaveClass('bg-paper-tab')
    expect(within(list).getAllByRole('listitem')).toHaveLength(5)
    await user.click(buttons[1])
    expect(buttons[1]).toHaveAttribute('aria-pressed', 'true')
    expect(within(list).getAllByRole('link').map((link) => link.textContent)).toEqual(['Sort user feedback for this week', 'Task waiting for a user answer'])
    await user.click(buttons[2])
    expect(within(list).getAllByRole('link').map((link) => link.textContent)).toEqual(['Export timeout fix plan'])
    await user.click(buttons[3])
    expect(within(list).getAllByRole('link').map((link) => link.textContent)).toEqual(['Weekly report draft'])
  })

  it('while more pages are unread, the counts based on the tasks read so far get a “+”', async () => {
    await renderList(undefined, 'next')
    await waitFor(() => expect(within(tabs()).getAllByRole('button').map((button) => button.textContent)).toEqual(['All5+', 'In progress2', 'Blocked1', 'Completed1+']))
  })

  it('one row per task: a title with a subtitle (the reason), the status (running a fluorescent-pink pill, blocked a tilted red stamp, completed a light-green pill), the current node in monospace, the responsible Agent, and the update time in monospace', async () => {
    await renderList()
    const running = rowOf('Sort user feedback for this week')
    expect(running).toHaveClass('border-b-[1.5px]', 'border-dashed')
    expect(within(running).getByText('Running')).toHaveClass('bg-riso-pink', 'rounded-full')
    expect(within(running).getByText('triage')).toHaveClass('font-mono', 'bg-tint-blue')
    expect(running.querySelector('time')).toHaveClass('font-mono')
    const blocked = rowOf('Export timeout fix plan')
    expect(within(blocked).getByText('Continues once you confirm the timeout limit')).toBeInTheDocument()
    const stamp = within(blocked).getByText('Blocked').closest('[data-task-state]')!
    expect(stamp).toHaveAttribute('data-task-state', 'blocked')
    expect(stamp).toHaveClass('riso-tilt', 'text-red-text')
    expect(within(rowOf('Weekly report draft')).getByText('Completed')).toHaveClass('bg-tint-green', 'text-green-text')
    // The responsible Agent has an avatar (two small overprinted circles).
    expect(within(running).getByText('task-checker').previousElementSibling).toHaveAttribute('aria-hidden', 'true')
  })

  it('the number is the first column: the header says “No.” and each row has a monospace bold light-ink “#12” in the same group as the title, so narrow screens do not split them', async () => {
    const { list } = await renderList()
    const header = list.previousElementSibling!
    expect(header).toHaveAttribute('aria-hidden', 'true')
    expect(header.firstElementChild).toHaveTextContent(/^No\.$/)
    expect(header.firstElementChild).toHaveClass('flex-[0_0_34px]')
    const running = rowOf('Sort user feedback for this week')
    // First column, so screen readers read the column name first; the number and the title sit in the same non-wrapping group.
    const group = running.firstElementChild!
    expect(group).toHaveClass('flex-nowrap')
    expect(group).toContainElement(within(running).getByRole('link', { name: 'Sort user feedback for this week' }))
    const number = group.firstElementChild!
    expect(number).toHaveTextContent(/^No\.#12$/)
    expect(number).toHaveClass('flex-[0_0_34px]', 'font-mono', 'text-[12px]', 'font-bold', 'text-ink-500')
    expect(within(number as HTMLElement).getByText('No.')).toHaveClass('sr-only')
    expect(within(list).getAllByRole('listitem').map((row) => row.firstElementChild!.firstElementChild!.textContent)).toEqual(['No.#12', 'No.#11', 'No.#10', 'No.#9', 'No.#8'])
  })

  it('the “Progress” stub holds three big numbers for in progress, blocked and completed; hidden on narrow screens', async () => {
    await renderList()
    const stub = screen.getByRole('complementary', { name: 'Progress' })
    expect(stub).toHaveClass('max-nav:hidden')
    await waitFor(() => expect(within(stub).getByText('in progress').previousElementSibling).toHaveTextContent(/^2$/))
    expect(within(stub).getByText('blocked').previousElementSibling).toHaveTextContent(/^1$/)
    expect(within(stub).getByText('completed').previousElementSibling).toHaveTextContent(/^1$/)
    expect(within(stub).getByText('blocked').previousElementSibling).toHaveClass('text-red-text')
  })
})
