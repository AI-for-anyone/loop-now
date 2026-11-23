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
const tasksLink = (nav: HTMLElement = sidebar()) => within(nav).getByRole('link', { name: /^Tasks/ })

// Counts endpoint: returns a mutable count and records how many times it was read.
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

describe('running count next to “Tasks” in the sidebar', () => {
  it('counts only running: preparing, waiting and blocked do not count; screen readers read “N tasks running”', async () => {
    counts({ preparing: 2, running: 3, waiting: 4, blocked: 1 })
    renderRoutes(routes, folderPath)
    await waitFor(() => expect(tasksLink()).toHaveAccessibleName(/^Tasks\s*3 tasks running$/))
    expect(within(tasksLink()).getByText('3')).toHaveAttribute('aria-hidden', 'true')
  })

  it('not shown when running is 0 (even with preparing, waiting or blocked tasks)', async () => {
    const state = counts({ preparing: 1, running: 0, waiting: 2, blocked: 1 })
    renderRoutes(routes, folderPath)
    await waitFor(() => expect(state.reads).toBeGreaterThan(0))
    expect(tasksLink()).toHaveAccessibleName('Tasks')
    expect(tasksLink().textContent).toBe('Tasks')
  })

  it('the running count comes before the “Needs your reply” sticker when both show', async () => {
    counts(
      { preparing: 0, running: 2, waiting: 0, blocked: 0 },
      { questions: [], unread: [], byAgent: [], byTask: [{ taskId: crypto.randomUUID(), projectId: defaultProject.id, questions: 1, unread: 0 }] },
    )
    renderRoutes(routes, folderPath)
    const link = await waitFor(() => {
      const found = tasksLink()
      expect(found).toHaveTextContent('2 tasks running')
      expect(found).toHaveTextContent('Needs your reply')
      return found
    })
    const running = within(link).getByText('2 tasks running'), pending = within(link).getByText(/Needs your reply/, { selector: '.sr-only' })
    expect(running.compareDocumentPosition(pending) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('rereads at once when the tasks page receives a task change, without waiting for the periodic refresh', async () => {
    const state = counts({ preparing: 0, running: 1, waiting: 0, blocked: 0 })
    const detail = makeTaskDetail(defaultProject.id, defaultProject.name)
    server.use(http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks: [detail.task], nextCursor: null })))
    renderRoutes(routes, tasksPath(defaultProject.name))
    act(() => FakeEventSource.instances.forEach((source) => source.open()))
    await waitFor(() => expect(tasksLink()).toHaveAccessibleName(/^Tasks\s*1 task running$/))

    state.value = { preparing: 0, running: 0, waiting: 0, blocked: 0 }
    const stream = FakeEventSource.instances.find((source) => source.url.endsWith(`/projects/${defaultProject.id}/tasks/events`))!
    act(() => stream.emit('task.updated', { task: { ...detail.task, status: 'completed', version: detail.task.version + 1 } }, '9'))
    // The periodic refresh follows the Agent list's interval (several seconds), so an update
    // within waitFor's default 1 second can only come from a re-read triggered by the event.
    await waitFor(() => expect(tasksLink()).toHaveAccessibleName('Tasks'))
  })

  it('the global scope has no “Tasks” item and does not read the count', async () => {
    const state = counts({ preparing: 0, running: 5, waiting: 0, blocked: 0 })
    renderRoutes(routes, '/settings')
    await within(sidebar()).findByRole('link', { name: 'Global settings' })
    expect(within(sidebar()).queryByRole('link', { name: /^Tasks/ })).toBeNull()
    expect(state.reads).toBe(0)
  })

  it('shows in the narrow-screen drawer too', async () => {
    counts({ preparing: 0, running: 4, waiting: 0, blocked: 0 })
    renderRoutes(routes, folderPath)
    await waitFor(() => expect(tasksLink()).toHaveAccessibleName(/^Tasks\s*4 tasks running$/))
    await userEvent.setup().click(screen.getByRole('button', { name: 'Open navigation' }))
    const drawer = await screen.findByRole('dialog', { name: 'Main navigation' })
    expect(tasksLink(drawer)).toHaveAccessibleName(/^Tasks\s*4 tasks running$/)
  })
})
