import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { tasksPath } from '@/api/tasks'
import { routes } from '@/app/routes'
import { apiUrl, defaultProject, projectsUrl } from '@/test/api'
import { FakeEventSource } from '@/test/fake-event-source'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, sidebar } from '@/test/shell'
import { makeTaskDetail } from '@/test/tasks'

const folderPath = `/p/${encodeURIComponent(defaultProject.name)}/folder`
const tasksLink = (nav: HTMLElement = sidebar()) => within(nav).getByRole('link', { name: /^任务/ })

// 计数接口：返回可变的计数，并记下被读了几次。
function counts(initial: Schemas['TaskCounts'], pending: Schemas['InboxPending'] = { questions: [], unread: [], byAgent: [], byTask: [] }) {
  const state = { value: initial, reads: 0 }
  server.use(
    ...baseHandlers([]),
    http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.get(apiUrl('/inbox/pending'), () => HttpResponse.json(pending)),
    http.get(apiUrl(`/projects/${defaultProject.id}/tasks/counts`), () => {
      state.reads++
      return HttpResponse.json(state.value)
    }),
  )
  return state
}

describe('左侧栏“任务”旁的运行数', () => {
  it('只算 running：准备中、等待、阻塞的不计入；读屏读“N 个任务正在运行”', async () => {
    counts({ preparing: 2, running: 3, waiting: 4, blocked: 1 })
    renderRoutes(routes, folderPath)
    await waitFor(() => expect(tasksLink()).toHaveAccessibleName(/^任务\s*3 个任务正在运行$/))
    expect(within(tasksLink()).getByText('3')).toHaveAttribute('aria-hidden', 'true')
  })

  it('running 为 0 时不显示（即使有准备中、等待或阻塞的任务）', async () => {
    const state = counts({ preparing: 1, running: 0, waiting: 2, blocked: 1 })
    renderRoutes(routes, folderPath)
    await waitFor(() => expect(state.reads).toBeGreaterThan(0))
    expect(tasksLink()).toHaveAccessibleName('任务')
    expect(tasksLink().textContent).toBe('任务')
  })

  it('与“待你回复”贴纸并排时运行数在前', async () => {
    counts(
      { preparing: 0, running: 2, waiting: 0, blocked: 0 },
      { questions: [], unread: [], byAgent: [], byTask: [{ taskId: crypto.randomUUID(), projectId: defaultProject.id, questions: 1, unread: 0 }] },
    )
    renderRoutes(routes, folderPath)
    const link = await waitFor(() => {
      const found = tasksLink()
      expect(found).toHaveTextContent('2 个任务正在运行')
      expect(found).toHaveTextContent('待你回复')
      return found
    })
    const running = within(link).getByText('2 个任务正在运行'), pending = within(link).getByText(/待你回复/, { selector: '.sr-only' })
    expect(running.compareDocumentPosition(pending) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('任务页收到任务变化时立即重读，不等定时刷新', async () => {
    const state = counts({ preparing: 0, running: 1, waiting: 0, blocked: 0 })
    const detail = makeTaskDetail(defaultProject.id, defaultProject.name)
    server.use(http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks: [detail.task], nextCursor: null })))
    renderRoutes(routes, tasksPath(defaultProject.name))
    act(() => FakeEventSource.instances.forEach((source) => source.open()))
    await waitFor(() => expect(tasksLink()).toHaveAccessibleName(/^任务\s*1 个任务正在运行$/))

    state.value = { preparing: 0, running: 0, waiting: 0, blocked: 0 }
    const stream = FakeEventSource.instances.find((source) => source.url.endsWith(`/projects/${defaultProject.id}/tasks/events`))!
    act(() => stream.emit('task.updated', { task: { ...detail.task, status: 'completed', version: detail.task.version + 1 } }, '9'))
    // 定时刷新按 Agent 列表的间隔（数秒），这里在 waitFor 的默认 1 秒内更新只能来自事件触发的重读。
    await waitFor(() => expect(tasksLink()).toHaveAccessibleName('任务'))
  })

  it('全局范围没有“任务”一项，也不读计数', async () => {
    const state = counts({ preparing: 0, running: 5, waiting: 0, blocked: 0 })
    renderRoutes(routes, '/settings')
    await within(sidebar()).findByRole('link', { name: '全局设置' })
    expect(within(sidebar()).queryByRole('link', { name: /^任务/ })).toBeNull()
    expect(state.reads).toBe(0)
  })

  it('窄屏的抽屉里同样显示', async () => {
    counts({ preparing: 0, running: 4, waiting: 0, blocked: 0 })
    renderRoutes(routes, folderPath)
    await waitFor(() => expect(tasksLink()).toHaveAccessibleName(/^任务\s*4 个任务正在运行$/))
    await userEvent.setup().click(screen.getByRole('button', { name: '打开导航' }))
    const drawer = await screen.findByRole('dialog', { name: '主导航' })
    expect(tasksLink(drawer)).toHaveAccessibleName(/^任务\s*4 个任务正在运行$/)
  })
})
