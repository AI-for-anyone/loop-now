import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { tasksPath } from '@/api/tasks'
import { apiUrl, defaultProject, projectsUrl } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { makeTaskDetail } from '@/test/tasks'
import { branchParts } from './branch-name'

const long = 'task/15-truncate-long-branch-names-in-the-middle-so-both-ends'

function list() {
  const base = makeTaskDetail(defaultProject.id, defaultProject.name).task
  const tasks = [
    { ...base, id: '00000000-0000-7000-8000-000000000301', number: 13, title: 'With a branch', branch: long, workspaceMode: 'git' as const },
    { ...base, id: '00000000-0000-7000-8000-000000000302', number: 14, title: 'Without a branch', branch: null },
    { ...base, id: '00000000-0000-7000-8000-000000000303', number: 15, title: 'On a folder copy', branch: null, workspaceMode: 'copy' as const },
  ]
  server.use(...baseHandlers([]), http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks, nextCursor: null })))
  renderRoutes(routes, tasksPath(defaultProject.name))
}
const row = async (title: string) => (await screen.findByRole('link', { name: title })).closest('li')!

describe('the branch in the task list', () => {
  it('a task with a branch shows it in its row, whole for screen readers and on hover', async () => {
    list()
    const branch = within(await row('With a branch')).getByTestId('task-branch')
    expect(branch).toHaveAttribute('title', long)
    expect(branch).toHaveClass('font-mono')
    expect(within(branch).getByText(`Branch ${long}`)).toHaveClass('sr-only')
  })
  it('a row without a workspace stays as it was', async () => {
    list()
    const plain = await row('Without a branch')
    expect(within(plain).queryByTestId('task-branch')).not.toBeInTheDocument()
    expect(within(plain).queryByText('Folder copy')).not.toBeInTheDocument()
  })
  it('a task on a folder copy shows a small Folder copy label where a branch would be', async () => {
    list()
    const copied = await row('On a folder copy')
    expect(within(copied).getByText('Folder copy')).toBeVisible()
    expect(within(copied).queryByTestId('task-branch')).not.toBeInTheDocument()
    expect(within(await row('With a branch')).queryByText('Folder copy')).not.toBeInTheDocument()
  })
  it('long names keep their end: the start gives way first', () => {
    expect(branchParts(long)).toEqual({ head: 'task/15-truncate-long-branch-names-in-the-middle-', tail: 'so-both-ends' })
    expect(branchParts('task/12-x')).toEqual({ head: '', tail: 'task/12-x' })
    expect(branchParts(long).head + branchParts(long).tail).toBe(long)
  })
})
