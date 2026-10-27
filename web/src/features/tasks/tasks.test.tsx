import { taskInboxReceipt } from '@/test/task-inbox'
import { taskInboxText as inboxCopy } from '@/i18n/task-inbox'
import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { renderRoutes, renderHookWithQuery } from '@/test/render'
import { apiUrl, defaultProject, errorBody, projectsUrl } from '@/test/api'
import { baseHandlers } from '@/test/shell'
import { server } from '@/test/server'
import { FakeEventSource } from '@/test/fake-event-source'
import { makeTaskDetail, makeTaskMessage, nodeId, oldNodeId, taskId } from '@/test/tasks'
import { taskKeys, tasksPath, useTaskMessages } from '@/api/tasks'
import { lastScope } from '@/lib/scope'
import type { Schemas } from '@/api/client'
import { acceptTaskEvent } from './useTaskLive'

function setup(initial?: Schemas['TaskDetail']) {
  let detail = initial ?? makeTaskDetail(defaultProject.id, defaultProject.name)
  let message = makeTaskMessage()
  const calls: { node: string; cursor: string | null }[] = []
  server.use(...baseHandlers([]), http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks: [detail.task], nextCursor: null })),
    http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: String(params.node), agentId: detail.nodes[1].agentId, runtimeId: detail.nodes[1].runtimeId, canSend: false, reason: '原任务实例暂不可用', handlingHint: '请检查当前状态' })),
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
async function renderDetail() {
  const env = setup()
  const result = renderRoutes(routes, detailPath())
  await screen.findByRole('heading', { name: '通用任务验收' })
  env.open()
  await screen.findByText('任务实例的回复')
  return { ...env, env, ...result, user: userEvent.setup() }
}

describe('项目任务', () => {
  it('列表只显示项目事实，责任Agent不代表执行中，左侧栏的任务是当前项', async () => {
    const env = setup(); renderRoutes(routes, tasksPath(defaultProject.name)); env.open()
    expect(await screen.findByRole('link', { name: '通用任务验收' })).toHaveAttribute('href', detailPath())
    const nav = screen.getByRole('navigation', { name: '主导航' })
    expect(within(nav).getAllByRole('link').filter((l) => l.getAttribute('aria-current') === 'page').map((l) => l.textContent)).toEqual(['任务'])
    expect(screen.getByText('阻塞')).toBeVisible()
  })
  it('平台先选择项目，不默选默认项目', async () => {
    setup(); lastScope.set('platform'); const { router } = renderRoutes(routes, '/tasks')
    expect(await screen.findByRole('dialog')).toHaveTextContent('选择项目查看任务')
    expect(router.state.location.pathname).toBe('/tasks')
    await userEvent.click(screen.getByRole('link', { name: defaultProject.name }))
    await waitFor(() => expect(router.state.location.pathname).toBe(tasksPath(defaultProject.name)))
  })
  it('缺省任务入口使用最近项目', async () => {
    setup(); lastScope.set(defaultProject.id); const { router } = renderRoutes(routes, '/tasks')
    await screen.findByRole('link', { name: '通用任务验收' })
    expect(router.state.location.pathname).toBe(tasksPath(defaultProject.name))
  })
  it('错误项目不显示任务详情', async () => {
    const d = makeTaskDetail('other-project'); setup(d); renderRoutes(routes, detailPath())
    expect(await screen.findByRole('alert')).toHaveTextContent('这个任务不属于当前项目')
    expect(screen.queryByRole('button', { name: '重试当前节点' })).not.toBeInTheDocument()
  })
  it('空列表没有新建任务操作', async () => {
    setup(); server.use(http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks: [], nextCursor: null })))
    renderRoutes(routes, tasksPath(defaultProject.name)); expect(await screen.findByText(/还没有任务/)).toBeVisible()
    expect(screen.queryByRole('button', { name: /新建任务/ })).not.toBeInTheDocument()
  })
})

describe('节点记录与更新', () => {
  it('initial record and graph navigation preserve the original task action locator', async () => {
    setup()
    const key = `taskLarkSwitch.${taskId}`, original = '30000000-0000-7000-8000-000000000001'
    const { router } = renderRoutes(routes, `${detailPath()}?${key}=${original}`)
    await screen.findByText('任务实例的回复')
    await waitFor(() => expect(new URLSearchParams(router.state.location.search).get('record')).toBe(nodeId))
    expect(new URLSearchParams(router.state.location.search).get(key)).toBe(original)
    await userEvent.click(screen.getByRole('button', { name: /提交报告/ }))
    await waitFor(() => expect(new URLSearchParams(router.state.location.search).get('step')).toBe('finish'))
    expect(new URLSearchParams(router.state.location.search).get(key)).toBe(original)
    expect(new URLSearchParams(router.state.location.search).has('record')).toBe(false)
    await userEvent.click(screen.getByRole('button', { name: /质量检查/ }))
    await waitFor(() => expect(new URLSearchParams(router.state.location.search).get('record')).toBe(nodeId))
    expect(new URLSearchParams(router.state.location.search).get(key)).toBe(original)
  })
  it('同step连续历次对话、分组资料与旧record锚点保留，不因SSE切换', async () => {
    const { user, router, env } = await renderDetail()
    expect(screen.getByRole('tab', { name: '对话' })).toHaveAttribute('aria-selected', 'true')
    expect(await screen.findByText('历史节点回复')).toBeVisible()
    expect(screen.getByText('任务实例的回复')).toBeVisible()
    await user.click(screen.getByRole('button', { name: /执行分界 · 第 1 次访问/ }))
    expect(screen.getByRole('tab', { name: '节点资料' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByText(/历史成功结果/)).toBeVisible()
    expect(screen.getByText(/仅供检查，不作为成功结果/)).toBeVisible()
    await user.click(screen.getByRole('link', { name: /输入 text 的来源/ }))
    expect(router.state.location.search).toContain(oldNodeId)
    expect(screen.getByText('任务实例的回复')).toBeVisible()
    expect(screen.getByRole('textbox', { name: '消息草稿' })).toHaveAttribute('readonly')
    expect(screen.queryByRole('button', { name: '重试当前节点' })).not.toBeInTheDocument()
    env.detail = { ...env.detail, task: { ...env.detail.task, status: 'running', version: 3 } }
    env.emit('task.updated', { task: env.detail.task }, '2')
    await waitFor(() => expect(screen.getByText('查看当前节点')).toBeVisible())
    expect(router.state.location.search).toContain(oldNodeId)
    expect(env.calls.every(c => c.node === nodeId || c.node === oldNodeId)).toBe(true)
  })
  it('节点会话的滚动区是定位容器，里面绝对定位的说明文字不会撑高整页', async () => {
    await renderDetail()
    expect(document.querySelector('[data-conversation-scroll]')).toHaveClass('relative')
  })
  it('消息同ID全文更新后不重复气泡，十进制事件ID不会按字典序比较', async () => {
    const { env } = await renderDetail()
    env.message = { ...env.message, text: '更新后的最终文字' }
    env.emit('message.created', env.message, '10')
    expect(await screen.findByText('更新后的最终文字')).toBeVisible()
    env.emit('message.created', env.message, '10')
    expect(screen.getAllByText('更新后的最终文字')).toHaveLength(1)
    expect(acceptTaskEvent('9', '10')).toBe(true); expect(acceptTaskEvent('10', '9')).toBe(false)
  })
  it('关闭问题不开放回答，成果下载使用平台路径，归档状态独立显示', async () => {
    const d = makeTaskDetail(defaultProject.id); d.cleanup = 'archived'; d.task.status = 'cancelled'; d.nodes[1].question = { id: nodeId, threadId: taskId, messageId: oldNodeId, text: '确认结果吗', status: 'closed', canAnswer: false }
    setup(d); renderRoutes(routes, detailPath()); await screen.findByRole('heading', { name: '通用任务验收' }); await userEvent.click(screen.getByText('待回答问题 · 已关闭')); expect(screen.getByText('确认结果吗')).toBeVisible()
    expect(screen.queryByRole('link', { name: '前往回答' })).not.toBeInTheDocument()
    expect(screen.getByText('记录已归档，副本已回收')).toBeVisible()
    await userEvent.click(screen.getByRole('tab', { name: '节点资料' })); await userEvent.click(screen.getByText('第 1 次访问 · 第 1 次尝试 · 已完成')); expect(screen.getByRole('link', { name: '下载' })).toHaveAttribute('href', apiUrl(d.artifacts[0].downloadPath))
  })
  it('已选记录失效明确说明，不静默切到另一条', async () => {
    setup(); renderRoutes(routes, detailPath() + '?record=missing')
    expect(await screen.findByText('找不到指定记录，请重新选择节点。')).toBeVisible()
    expect(screen.queryByText('任务实例的回复')).not.toBeInTheDocument()
  })
  it('失联禁用控制操作，焦点刷新补齐事实', async () => {
    const { env, client } = await renderDetail()
    const es = FakeEventSource.instances.find((s) => s.url.endsWith(`/tasks/${taskId}/events`))!
    act(() => es.interrupt()); expect(screen.getByRole('button', { name: '重试当前节点' })).toBeDisabled()
    env.detail = { ...env.detail, cleanup: 'pending' }
    act(() => window.dispatchEvent(new Event('focus')))
    await waitFor(() => expect(client.getQueryData<Schemas['TaskDetail']>(taskKeys.detail(taskId))?.cleanup).toBe('pending'))
  })
})

describe('控制动作', () => {
  it('重试保留说明，陈旧目标不改投新节点，写动作不自动重试', async () => {
    const { env, user } = await renderDetail(); const bodies: Schemas['TaskRetryInput'][] = []
    server.use(http.post(apiUrl(`/tasks/${taskId}/retry`), async ({ request }) => { bodies.push(await request.json() as Schemas['TaskRetryInput']); return HttpResponse.json(errorBody('task_conflict', '旧记录已变化'), { status: 409 }) }))
    await user.click(screen.getByRole('button', { name: '重试当前节点' })); const dialog = screen.getByRole('alertdialog')
    await user.type(within(dialog).getByRole('textbox'), '保留这段说明')
    env.detail = { ...env.detail, task: { ...env.detail.task, currentNodeRunId: oldNodeId, version: 3 } }
    env.emit('task.updated', { task: env.detail.task }, '3')
    await user.click(within(dialog).getByRole('button', { name: '重试当前节点' }))
    await within(dialog).findByText(/旧记录已变化/)
    expect(within(dialog).getByRole('textbox')).toHaveValue('保留这段说明')
    expect(bodies).toHaveLength(1); expect(bodies[0].expectedNodeRunId).toBe(nodeId); expect(bodies[0].note).toBe('保留这段说明')
  })
  it('取消后仍显示归档处理中，不把已接受冒充清理完成', async () => {
    const { env, user } = await renderDetail(); let calls = 0
    server.use(http.post(apiUrl(`/tasks/${taskId}/cancel`), async ({ request }) => { expect(await request.json()).toHaveProperty('actionId'); calls++; env.detail = { ...env.detail, task: { ...env.detail.task, status: 'cancelled', version: 3 }, cleanup: 'pending' }; return HttpResponse.json(env.detail) }))
    await user.click(screen.getByRole('button', { name: '取消任务' })); await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: '取消任务' }))
    expect(await screen.findByText('正在归档与回收')).toBeVisible(); expect(calls).toBe(1)
    expect(screen.queryByRole('button', { name: '重试当前节点' })).not.toBeInTheDocument()
  })
})

it('任务会话按任务与nodeRun隔离，分页按cursor继续且同ID只保留最新值', async () => {
  const env = setup()
  const otherTask = '00000000-0000-7000-8000-000000000555'
  server.use(http.get(apiUrl(`/tasks/${otherTask}/nodes/${nodeId}/messages`), () => HttpResponse.json({ messages: [makeTaskMessage(nodeId, '另一任务的消息')], nextCursor: null })))
  const { result } = renderHookWithQuery(() => ({ first: useTaskMessages(taskId, nodeId), second: useTaskMessages(otherTask, nodeId) }))
  await waitFor(() => expect(result.current.second.data?.pages[0].messages[0].text).toBe('另一任务的消息'))
  expect(result.current.first.data?.pages[0].messages[0].text).toBe(env.message.text)
})
it('节点消息分页读取后用同ID更新，不把其他节点的消息混入当前记录', async () => {
  const env = setup()
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), ({ request }) => {
    const next = new URL(request.url).searchParams.has('cursor')
    return HttpResponse.json(next ? { messages: [{ ...env.message, text: '分页后的更新' }, makeTaskMessage(oldNodeId, '不属于当前节点')], nextCursor: null } : { messages: [env.message], nextCursor: 'page-2' })
  }))
  renderRoutes(routes, detailPath()); await screen.findByText('任务实例的回复')
  await userEvent.click(screen.getByRole('button', { name: '加载更多' }))
  expect(await screen.findByText('分页后的更新')).toBeVisible()
  expect(screen.queryByText('任务实例的回复')).not.toBeInTheDocument()
  expect(screen.queryByText('不属于当前节点')).not.toBeInTheDocument()
})
it('项目任务事件能发现新任务，重连后重取列表', async () => {
  const env = setup(); let summaries = [env.detail.task]
  server.use(http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks: summaries, nextCursor: null })))
  renderRoutes(routes, tasksPath(defaultProject.name)); await screen.findByRole('link', { name: '通用任务验收' })
  const stream = FakeEventSource.instances.find((s) => s.url.endsWith(`/projects/${defaultProject.id}/tasks/events`))!
  act(() => stream.open()); summaries = [...summaries, { ...env.detail.task, id: oldNodeId, title: 'Manager新建的任务' }]
  act(() => stream.emit('task.updated', { task: summaries[1] }, '9'))
  expect(await screen.findByRole('link', { name: 'Manager新建的任务' })).toBeVisible()
  summaries = [{ ...env.detail.task, title: '断线期间更新的任务' }]
  act(() => { stream.interrupt(); stream.open() })
  expect(await screen.findByRole('link', { name: '断线期间更新的任务' })).toBeVisible()
})
it('项目改名按稳定ID跟随，保留任务及节点记录的深链接', async () => {
  const { client, router } = await renderDetail()
  server.use(http.get(projectsUrl, () => HttpResponse.json({ projects: [{ ...defaultProject, name: '任务项目改名' }] })))
  await act(() => client.invalidateQueries({ queryKey: ['projects'] }))
  await waitFor(() => expect(decodeURIComponent(router.state.location.pathname)).toContain('/p/任务项目改名/tasks/'))
  expect(router.state.location.search).toContain(nodeId)
})
it('横向画布节点可键盘选择，适应与定位控件在缩放层外', async () => {
  const detail = makeTaskDetail(defaultProject.id)
  detail.workflow.steps.push({ ...detail.workflow.steps[0], id: 'revise', title: '返工处理' })
  detail.workflow.edges.push({ id: 'branch', from: 'check', to: 'revise', label: '需要修订' }, { id: 'return', from: 'revise', to: 'check', label: '再次检查', maxTraversals: 2 })
  setup(detail); const { router } = renderRoutes(routes, detailPath())
  const list = await screen.findByLabelText('流程画布，可拖动或使用方向键平移')
  const button = within(list).getByRole('button', { name: /返工处理/ })
  button.focus(); await userEvent.keyboard('{Enter}')
  await waitFor(() => expect(router.state.location.search).toContain('step=revise'))
  expect(screen.getByText('这个节点尚未被访问。')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: '适应视图' }))
  expect(screen.getByRole('button', { name: '适应视图' }).closest('[data-task-camera]')).toBeNull(); expect(button.closest('[data-task-camera]')).not.toBeNull()
  expect(within(list).getAllByRole('button')).toHaveLength(3)
})
it('同一Agent执行另一个节点时切换消息范围，不沿用前一节点会话', async () => {
  const env = setup()
  const otherId = '00000000-0000-7000-8000-000000000401'
  const otherRun = '00000000-0000-7000-8000-000000000402'
  env.detail = { ...env.detail, nodes: [...env.detail.nodes, { ...env.detail.nodes[1], id: otherId, stepId: 'finish', status: 'completed', visit: 1, inputs: { from: '另一个节点输入' }, output: { report: '另一个节点结果' }, runIds: [otherRun] }] }
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${otherId}/messages`), () => HttpResponse.json({ messages: [{ ...makeTaskMessage(otherId, '同一Agent的另一个节点会话'), runId: otherRun }], nextCursor: null })))
  renderRoutes(routes, detailPath()); await screen.findByText('任务实例的回复')
  await userEvent.click(screen.getByRole('button', { name: /提交报告/ }))
  expect(await screen.findByText('同一Agent的另一个节点会话')).toBeVisible()
  expect(screen.queryByText('任务实例的回复')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('tab', { name: '节点资料' })); expect(screen.getByText(/另一个节点输入/)).toBeVisible()
})

it('消息可选错误字段显示服务端脱敏失败原因，兼容旧消息省略字段', async () => {
  const env = setup(); env.message = { ...env.message, status: 'failed', errorCode: 'pi_error', errorMessage: '服务端脱敏后的失败原因' }
  renderRoutes(routes, detailPath())
  expect(await screen.findByText('Agent 报告错误 · 服务端脱敏后的失败原因')).toBeVisible()
})

it.each([
  [404, 'not_found', '接口不存在', true],
  [404, 'not_found', '节点记录不存在', false],
  [401, 'unauthenticated', '需要控制台身份', false],
  [403, 'forbidden', '没有节点操作权限', false],
  [500, 'internal_error', '暂时无法检查目标', false],
] as const)('capability失败%s/%s安全禁发并保留草稿，只有接口缺失是旧服务', async (status, code, message, unsupported) => {
  const env = setup(); let gets = 0
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), () => { gets++; return HttpResponse.json(errorBody(code, message), { status }) }))
  renderRoutes(routes, detailPath()); await screen.findByText('任务实例的回复')
  const box = screen.getByRole('textbox', { name: '消息草稿' })
  await userEvent.type(box, '原节点草稿')
  expect(await screen.findByText(unsupported ? '当前服务尚未启用节点消息' : new RegExp(message))).toBeVisible()
  expect(screen.getByRole('button', { name: '发送' })).toBeDisabled()
  env.emit('node.updated', { node: env.detail.nodes[1] }, '20')
  await waitFor(() => expect(box).toHaveValue('原节点草稿'))
  if (unsupported) { await new Promise(resolve => setTimeout(resolve, 120)); expect(gets).toBe(1) }
  expect(screen.queryByText(unsupported ? '资源不存在或已删除：接口不存在' : '当前服务尚未启用节点消息')).not.toBeInTheDocument()
})

it('新attempt和任务结束保留原nodeRun草稿，只有手动选新执行才出现新空稿', async () => {
  const { env, user, router } = await renderDetail()
  await user.type(screen.getByRole('textbox', { name: '消息草稿' }), '只给旧执行的草稿')
  const next = { ...env.detail.nodes[1], id: '00000000-0000-7000-8000-000000000901', attempt: 2 }
  env.detail = { ...env.detail, nodes: [...env.detail.nodes, next], task: { ...env.detail.task, currentNodeRunId: next.id, version: 4 } }
  env.emit('node.updated', { node: next }, '21')
  await waitFor(() => expect(screen.getByRole('textbox', { name: '消息草稿' })).toHaveAttribute('readonly'))
  expect(screen.getByRole('textbox', { name: '消息草稿' })).toHaveValue('只给旧执行的草稿')
  expect(router.state.location.search).toContain(nodeId)
  await user.click(screen.getByRole('button', { name: '查看当前节点' }))
  expect(screen.getByRole('textbox', { name: '消息草稿' })).toHaveValue('')
  expect(screen.getByText('只给旧执行的草稿')).toBeInTheDocument()
  await user.type(screen.getByRole('textbox', { name: '消息草稿' }), '新执行自己的草稿')
  env.detail = { ...env.detail, task: { ...env.detail.task, status: 'completed', version: 5 } }
  env.emit('task.updated', { task: env.detail.task }, '22')
  await waitFor(() => expect(screen.getByRole('textbox', { name: '消息草稿' })).toHaveAttribute('readonly'))
  expect(screen.getByRole('textbox', { name: '消息草稿' })).toHaveValue('新执行自己的草稿')
})

it('SSE高频期间只串行dirty校准，不取消饥饿，校准后旧响应不覆盖新全文', async () => {
  const env = setup(); let count = 0, pending = 0, max = 0
  let release!: () => void
  const gate = new Promise<void>(resolve => { release = resolve })
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), async () => {
    count++; pending++; max = Math.max(max, pending)
    const current = count
    if (current === 2) await gate
    pending--
    return HttpResponse.json({ messages: [makeTaskMessage(nodeId, current < 3 ? '较早的全文' : '已校准的新全文')], nextCursor: null })
  }))
  renderRoutes(routes, detailPath()); await screen.findByText('较早的全文')
  env.emit('message.created', {}, '1')
  await waitFor(() => expect(count).toBe(2))
  for (let i = 2; i <= 25; i++) env.emit('message.created', {}, String(i))
  release()
  expect(await screen.findByText('已校准的新全文')).toBeVisible()
  expect(max).toBe(1); expect(count).toBe(3)
  expect(screen.queryByText('较早的全文')).not.toBeInTheDocument()
})

it('重连从首cursor校准所有已加载页，部分页不删除已看消息', async () => {
  const env = setup(); let refreshed = false; const cursors: (string | null)[] = []
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), ({ request }) => {
    const cursor = new URL(request.url).searchParams.get('cursor'); cursors.push(cursor)
    return HttpResponse.json(cursor ? { messages: [makeTaskMessage(nodeId, refreshed ? '旧页错误已校准' : '旧页原内容', 'older-page-id')], nextCursor: null } : { messages: refreshed ? [] : [env.message], nextCursor: 'independent-cursor' })
  }))
  renderRoutes(routes, detailPath()); await screen.findByText('任务实例的回复')
  await userEvent.click(screen.getByRole('button', { name: '加载更多' }))
  await screen.findByText('旧页原内容')
  refreshed = true; cursors.length = 0
  act(() => { const es = FakeEventSource.instances.find(s => s.url.endsWith(`/tasks/${taskId}/events`))!; es.interrupt(); es.open() })
  expect(await screen.findByText('旧页错误已校准')).toBeVisible()
  expect(cursors).toEqual([null, 'independent-cursor'])
  expect(screen.getByText('任务实例的回复')).toBeVisible()
})

it('两执行段各自加载超过50条，cursor和缺口互不借用，点击节点重新默认对话', async () => {
  setup(); const cursors: { node: string; cursor: string | null }[] = []
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/:node/messages`), ({ params, request }) => {
    const node = String(params.node), cursor = new URL(request.url).searchParams.get('cursor'); cursors.push({ node, cursor })
    return HttpResponse.json({ messages: Array.from({ length: cursor ? 2 : 50 }, (_, index) => makeTaskMessage(node, `${node === nodeId ? '当前段' : '历史段'}第${cursor ? 50 + index : index}条`, `${node}-${cursor ?? 'first'}-${index}`)), nextCursor: cursor ? null : `cursor-for-${node}` })
  }))
  renderRoutes(routes, detailPath()); await screen.findByText('当前段第49条'); await screen.findByText('历史段第49条')
  const gaps = screen.getAllByText('本次执行还有消息未加载。'); expect(gaps).toHaveLength(2)
  await userEvent.click(screen.getAllByRole('button', { name: '加载更多' })[0])
  await screen.findByText('历史段第51条')
  expect(cursors).toContainEqual({ node: oldNodeId, cursor: `cursor-for-${oldNodeId}` })
  expect(cursors).not.toContainEqual({ node: nodeId, cursor: `cursor-for-${oldNodeId}` })
  await userEvent.click(screen.getByRole('button', { name: '加载更多' }))
  await screen.findByText('当前段第51条')
  expect(cursors).toContainEqual({ node: nodeId, cursor: `cursor-for-${nodeId}` })
  await userEvent.click(screen.getByRole('tab', { name: '节点资料' }))
  await userEvent.click(screen.getByRole('button', { name: /质量检查/ }))
  expect(screen.getByRole('tab', { name: '对话' })).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByText('历史段第51条')).toBeVisible()
})

it('跨step来源链接定位原record而保留该step所有段，最新异常组默认展开', async () => {
  const env = setup(), thirdId = '00000000-0000-7000-8000-000000000903'
  const third = { ...env.detail.nodes[1], id: thirdId, stepId: 'finish', visit: 1, attempt: 1, inputVersions: [{ input: 'review', nodeRunId: oldNodeId }] }
  env.detail = { ...env.detail, nodes: [...env.detail.nodes, third], task: { ...env.detail.task, currentStepId: 'finish', currentNodeRunId: thirdId } }
  const { router } = renderRoutes(routes, detailPath())
  await screen.findByRole('heading', { name: '通用任务验收' })
  await userEvent.click(screen.getByRole('tab', { name: '节点资料' }))
  expect(screen.getByText(/仅供检查，不作为成功结果/)).toBeVisible()
  await userEvent.click(screen.getByRole('link', { name: /输入 review 的来源/ }))
  await waitFor(() => expect(router.state.location.search).toContain(`record=${oldNodeId}`))
  expect(router.state.location.search).toContain('step=check')
  expect(await screen.findByText('任务实例的回复')).toBeVisible()
  expect(screen.getByText('历史节点回复')).toBeVisible()
  expect(screen.getByRole('textbox', { name: '消息草稿' })).toHaveAttribute('readonly')
  await userEvent.click(screen.getByRole('tab', { name: '节点资料' }))
  expect(screen.getByText(/历史成功结果/)).toBeVisible()
})

it('等待状态保留平台事实、固定原因和原题权限，不显示历史重试', async () => {
  const detail = makeTaskDetail(defaultProject.id)
  detail.task.status = 'waiting'; detail.nodes[1].status = 'waiting'
  detail.nodes[1].reason = '等待Manager确认原问题'
  detail.nodes[1].question = { id: oldNodeId, messageId: oldNodeId, threadId: taskId, text: 'Manager的原问题', status: 'open', canAnswer: false }
  setup(detail); renderRoutes(routes, detailPath())
  await screen.findByRole('heading', { name: '通用任务验收' })
  const status = document.querySelector('[data-node-status]') as HTMLElement
  expect(within(status).getByText('等待回复')).toBeVisible()
  expect(within(status).getAllByText('等待Manager确认原问题')[0]).toBeVisible()
  await userEvent.click(within(status).getByText('待回答问题 · 待回答'))
  expect(within(status).getByText('当前身份不能回答这个问题。')).toBeVisible()
  expect(screen.queryByRole('link', { name: '前往回答' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '重试当前节点' })).not.toBeInTheDocument()
  const tab = screen.getByRole('tab', { name: '对话' }); tab.focus(); await userEvent.keyboard('{ArrowRight}')
  expect(screen.getByRole('tab', { name: '节点资料' })).toHaveFocus()
})

it('旧后端能力缺失后窗口focus重新取同一正式GET，可恢复而不改草稿或目标', async () => {
  setup(); let ready = false, count = 0
  const detail = makeTaskDetail(defaultProject.id)
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => { count++; return ready ? HttpResponse.json({ taskId, nodeRunId: String(params.node), agentId: detail.nodes[1].agentId, runtimeId: detail.nodes[1].runtimeId, canSend: false, reason: '原会话不可用', handlingHint: '请检查任务实例' }) : HttpResponse.json(errorBody('not_found', '接口不存在'), { status: 404 }) }))
  renderRoutes(routes, detailPath()); await screen.findByText('当前服务尚未启用节点消息')
  await userEvent.type(screen.getByRole('textbox', { name: '消息草稿' }), '升级前草稿')
  ready = true; act(() => window.dispatchEvent(new Event('focus')))
  expect(await screen.findByText('原会话不可用 · 请检查任务实例')).toBeVisible()
  expect(screen.queryByText('当前服务尚未启用节点消息')).not.toBeInTheDocument()
  expect(screen.getByRole('textbox', { name: '消息草稿' })).toHaveValue('升级前草稿')
  expect(count).toBe(2)
})

it('未知发送结果跨节点查看保留原动作，回原执行手动确认不新建action', async () => {
  const env = setup(); const bodies: Schemas['TaskNodeInboxInput'][] = []
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/:node/message-capability`), ({ params }) => HttpResponse.json({ taskId, nodeRunId: String(params.node), agentId: env.detail.nodes[1].agentId, runtimeId: env.detail.nodes[1].runtimeId, canSend: true, reason: null, handlingHint: null })),
    http.post(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/inbox-messages`), async ({ request }) => {
      const body = await request.json() as Schemas['TaskNodeInboxInput']; bodies.push(body)
      return bodies.length === 1 ? HttpResponse.json(errorBody('internal_error', '回执不可确认'), { status: 500 }) : HttpResponse.json(taskInboxReceipt(body), { status: 201 })
    }))
  renderRoutes(routes, detailPath()); await screen.findByText('任务实例的回复'); env.open()
  await waitFor(() => expect(screen.getByRole('button', { name: '发送' })).toBeEnabled())
  await userEvent.type(screen.getByRole('textbox', { name: '消息草稿' }), '原动作草稿')
  await userEvent.click(screen.getByRole('button', { name: '发送' })); await screen.findByRole('button', { name: '重试确认原消息' })
  await userEvent.click(screen.getByRole('button', { name: /提交报告/ })); await screen.findByText('这个节点尚未被访问。')
  await userEvent.click(screen.getByRole('button', { name: /质量检查/ }))
  const retry = await screen.findByRole('button', { name: '重试确认原消息' })
  expect(screen.getByRole('textbox', { name: '消息草稿' })).toHaveValue('原动作草稿')
  expect(bodies).toHaveLength(1)
  await waitFor(() => expect(retry).toBeEnabled()); await userEvent.click(retry)
  await screen.findByText(inboxCopy.accepted)
  expect(bodies).toHaveLength(2); expect(bodies[1]).toEqual(bodies[0])
})

it('console_node普通消息显示你，平台初始化与提醒来源保持，不把控制台冒充平台', async () => {
  setup()
  const messages = [
    { ...makeTaskMessage(nodeId, '普通控制台消息', 'console-message'), role: 'user', origin: 'console_node' },
    { ...makeTaskMessage(nodeId, '平台初始化消息', 'platform-message'), role: 'user', origin: 'task_node' },
    { ...makeTaskMessage(nodeId, 'Inbox提醒消息', 'reminder-message'), role: 'user', origin: 'inbox_reminder' },
  ]
  server.use(http.get(apiUrl(`/tasks/${taskId}/nodes/${nodeId}/messages`), () => HttpResponse.json({ messages, nextCursor: null })))
  renderRoutes(routes, detailPath())
  const consoleRow = (await screen.findByText('普通控制台消息')).closest('li')!
  expect(within(consoleRow).getByText('你')).toBeVisible(); expect(within(consoleRow).queryByText('平台')).not.toBeInTheDocument()
  expect(within(screen.getByText('平台初始化消息').closest('li')!).getByText('平台')).toBeVisible()
  expect(within(screen.getByText('Inbox提醒消息').closest('li')!).getByText(/平台提醒/)).toBeVisible()
})
