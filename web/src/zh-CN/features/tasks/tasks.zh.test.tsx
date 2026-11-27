import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { renderRoutes } from '@/test/render'
import { apiUrl, defaultProject, errorBody, projectsUrl } from '@/test/api'
import { baseHandlers } from '@/test/shell'
import { server } from '@/test/server'
import { FakeEventSource } from '@/test/fake-event-source'
import { makeTaskDetail, makeTaskMessage, nodeId, taskId } from '@/test/tasks'
import { tasksPath } from '@/api/tasks'
import type { Schemas } from '@/api/client'

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
      return HttpResponse.json({ messages: [node === nodeId ? message : makeTaskMessage(node, '历史节点回复')], nextCursor: null })
    }))
  const emit = (type: string, data: unknown, id = '1') => {
    const es = FakeEventSource.instances.find((s) => s.url.endsWith(`/tasks/${taskId}/events`))!
    act(() => es.emit(type, data, id))
  }
  const open = () => act(() => FakeEventSource.instances.forEach((es) => es.open()))
  return { get detail() { return detail }, set detail(next) { detail = next }, get message() { return message }, set message(next) { message = next }, calls, emit, open }
}
const detailPath = () => `${tasksPath(defaultProject.name)}/${taskId}`

// Older servers had no unknown_endpoint signal and answered a missing endpoint with the fixed Chinese text “接口不存在” (No such
// endpoint); that text alone marks the server as old. The other capability failures are in src/features/tasks/tasks.test.tsx.
it.each([
  [404, 'not_found', '接口不存在', true, undefined],
] as const)('capability failure %s/%s disables sending safely and keeps the draft; only a missing endpoint means an old server', async (status, code, message, unsupported, details) => {
  const env = setup(); let gets = 0
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), () => { gets++; return HttpResponse.json(errorBody(code, message, details ? { details: details as never } : {}), { status }) }))
  renderRoutes(routes, detailPath()); await screen.findByText('Reply from the task instance')
  const box = screen.getByRole('textbox', { name: '消息草稿' })
  await userEvent.type(box, '原节点草稿')
  expect(await screen.findByText(unsupported ? '当前服务尚未启用节点消息' : new RegExp(message))).toBeVisible()
  expect(screen.getByRole('button', { name: '发送' })).toBeDisabled()
  env.emit('node.updated', { node: env.detail.nodes[1] }, '20')
  await waitFor(() => expect(box).toHaveValue('原节点草稿'))
  if (unsupported) { await new Promise(resolve => setTimeout(resolve, 120)); expect(gets).toBe(1) }
  expect(screen.queryByText(unsupported ? `资源不存在或已删除：${message}` : '当前服务尚未启用节点消息')).not.toBeInTheDocument()
})

it('shows Chinese labels for the step prompt and conversation opt-in mark', async () => {
  const detail = makeTaskDetail(defaultProject.id, defaultProject.name)
  detail.workflow.steps[0].prompt = '### 执行说明\n\n- 先看 `PLAN.md`'
  detail.workflow.steps[0].withConversation = true
  setup(detail)
  renderRoutes(routes, detailPath())
  await screen.findByRole('heading', { level: 1, name: 'General task acceptance' })
  const user = userEvent.setup()

  const status = document.querySelector<HTMLElement>('[data-node-status]')!
  expect(within(status).getByText('带聊天记录')).toBeVisible()
  const promptRegion = within(status).getByRole('region', { name: '节点 Prompt' })
  const toggle = within(promptRegion).getByRole('button', { name: /节点 Prompt/ })
  expect(toggle).toHaveAttribute('aria-expanded', 'false')
  expect(toggle).toHaveTextContent('展开')
  await user.click(toggle)
  expect(toggle).toHaveAttribute('aria-expanded', 'true')
  expect(toggle).toHaveTextContent('收起')
  expect(within(promptRegion).getByRole('heading', { level: 3, name: '执行说明' })).toBeVisible()
})
