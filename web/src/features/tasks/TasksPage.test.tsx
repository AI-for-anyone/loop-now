import { screen, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { tasksPath } from '@/api/tasks'
import { apiUrl, defaultProject, makeAgent, projectsUrl } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { forkServerAgentId, forkWebAgentId, makeForkTaskDetail, makeTaskDetail } from '@/test/tasks'
import { branchParts } from './branch-name'

const long = 'task/15-truncate-long-branch-names-in-the-middle-so-both-ends'
const longTitle = 'A very long task title that truncates with an ellipsis on one line while the full text stays in the tooltip'
const longAgentId = '00000000-0000-7000-8000-000000000199'
const longAgentName = 'platform-release-verification-specialist'

function list() {
  const base = makeTaskDetail(defaultProject.id, defaultProject.name).task
  const parallel = makeForkTaskDetail(defaultProject.id, defaultProject.name).task
  const tasks = [
    { ...base, id: '00000000-0000-7000-8000-000000000301', number: 13, title: 'With a branch', reason: null, branch: long, workspaceMode: 'git' as const, currentStepId: 'implement', currentStepTitle: 'Implement' },
    { ...base, id: '00000000-0000-7000-8000-000000000302', number: 14, title: 'Without a branch', reason: null, branch: null, currentStepId: 'verify', currentStepTitle: null },
    { ...base, id: '00000000-0000-7000-8000-000000000303', number: 15, title: 'On a folder copy', reason: null, branch: null, workspaceMode: 'copy' as const, currentStepId: null, currentStepTitle: null },
    { ...base, id: '00000000-0000-7000-8000-000000000304', number: 16, title: longTitle, reason: null, branch: long, workspaceMode: 'git' as const, currentAgentId: longAgentId, currentStepId: 'pr', currentStepTitle: 'Pull request' },
    { ...parallel, id: '00000000-0000-7000-8000-000000000305', number: 17, title: 'Parallel branches task' },
  ]
  const agents = [
    makeAgent({ id: base.currentAgentId!, name: 'loop-now-main' }),
    makeAgent({ id: longAgentId, name: longAgentName }),
    makeAgent({ id: forkServerAgentId, name: 'backend-dev' }),
    makeAgent({ id: forkWebAgentId, name: 'frontend-dev' }),
  ]
  server.use(
    ...baseHandlers(agents),
    http.get(projectsUrl, () => HttpResponse.json({ projects: [defaultProject] })),
    http.get(apiUrl(`/projects/${defaultProject.id}/tasks`), () => HttpResponse.json({ tasks, nextCursor: null })),
  )
  renderRoutes(routes, tasksPath(defaultProject.name))
}
const row = async (title: string) => (await screen.findByRole('link', { name: title })).closest('li')!

describe('compact task list rows', () => {
  it('one-line rows: the title truncates with the full title in the tooltip, and the branch sits inline in muted monospace', async () => {
    list()
    const item = await row('With a branch')
    const link = within(item).getByRole('link', { name: 'With a branch' })
    expect(link).toHaveAttribute('title', 'With a branch')
    expect(link).toHaveClass('truncate')
    const branch = within(item).getByTestId('task-branch')
    expect(branch).toHaveAttribute('title', long)
    expect(branch).toHaveClass('font-mono', 'text-ink-500')
    expect(within(branch).getByText(`Branch ${long}`)).toHaveClass('sr-only')
    // Title and branch share the same single-line grid container (title gets minmax(0,max-content) first; branch takes the leftover 1fr).
    const cell = link.parentElement!.parentElement!
    expect(cell).toHaveClass('grid', 'grid-cols-[minmax(0,max-content)_minmax(0,1fr)]')
    expect(branch.parentElement).toBe(cell)
  })

  it('a row without a workspace has neither branch nor Folder copy label', async () => {
    list()
    const plain = await row('Without a branch')
    expect(within(plain).queryByTestId('task-branch')).not.toBeInTheDocument()
    expect(within(plain).queryByText('Folder copy')).not.toBeInTheDocument()
  })

  it('a task on a folder copy shows a small Folder copy label in the trailing space after the title so the title wins first', async () => {
    list()
    const copied = await row('On a folder copy')
    const link = within(copied).getByRole('link', { name: 'On a folder copy' })
    const label = within(copied).getByText('Folder copy')
    expect(label).toBeVisible()
    const cell = link.parentElement!.parentElement!
    expect(cell).toHaveClass('grid', 'grid-cols-[minmax(0,max-content)_minmax(0,1fr)]')
    expect(label.parentElement).toHaveClass('@container', 'min-w-0', 'overflow-hidden')
    expect(label.parentElement!.parentElement).toBe(cell)
    expect(label).toHaveClass('@max-[84px]:hidden')
    expect(within(copied).queryByTestId('task-branch')).not.toBeInTheDocument()
    expect(within(await row('With a branch')).queryByText('Folder copy')).not.toBeInTheDocument()
  })

  it('the current step shows its title with the id in the tooltip, falls back to the id when title is null, and shows a dash when empty', async () => {
    list()
    const titled = await row('With a branch')
    const chip = within(titled).getByText('Implement')
    expect(chip).toHaveAttribute('title', 'implement')
    expect(chip).toHaveClass('truncate', 'bg-surface-subtle')
    expect(chip).not.toHaveClass('font-mono')
    expect(within(titled).queryByText('implement')).not.toBeInTheDocument()

    const fallback = await row('Without a branch')
    const fallbackChip = within(fallback).getByText('verify')
    expect(fallbackChip).toHaveAttribute('title', 'verify')
    expect(fallbackChip).toHaveClass('font-mono')

    const none = await row('On a folder copy')
    expect(within(none).getByText('—')).toBeVisible()
  })

  it('with a long title and a long branch, the title wins the space first while the branch shrinks into the leftover track and hides when too narrow', async () => {
    list()
    const item = await row(longTitle)
    const link = within(item).getByRole('link', { name: longTitle })
    expect(link).toHaveAttribute('title', longTitle)
    expect(link).toHaveClass('truncate')

    const cell = link.parentElement!.parentElement!
    expect(cell).toHaveClass('grid', 'grid-cols-[minmax(0,max-content)_minmax(0,1fr)]')

    const branch = within(item).getByTestId('task-branch')
    expect(branch.parentElement).toBe(cell)
    expect(branch).toHaveAttribute('title', long)
    expect(branch).toHaveClass('@container', 'min-w-0', 'overflow-hidden')
    expect(branch).not.toHaveClass('shrink-0')
    expect(within(branch).getByText(`Branch ${long}`)).toHaveClass('sr-only')
    const visual = branch.querySelector('[aria-hidden="true"]')!
    expect(visual).toHaveClass('@max-[108px]:hidden')

    const agent = await within(item).findByText(longAgentName)
    expect(agent).toHaveAttribute('title', longAgentName)
    expect(agent).toHaveClass('truncate')
    expect(agent.parentElement).toHaveClass('min-w-0')

    // Header columns align with row columns via the shared grid track definition.
    const header = item.closest('ul')!.previousElementSibling!
    expect(item).toHaveClass('sm:grid', 'sm:grid-cols-[48px_minmax(0,1fr)_96px_116px_136px_84px]')
    expect(header).toHaveClass('sm:grid', 'sm:grid-cols-[48px_minmax(0,1fr)_96px_116px_136px_84px]')
  })

  it('long branch names keep their end: the start gives way first', () => {
    expect(branchParts(long)).toEqual({ head: 'task/15-truncate-long-branch-names-in-the-middle-', tail: 'so-both-ends' })
    expect(branchParts('task/12-x')).toEqual({ head: '', tail: 'task/12-x' })
    expect(branchParts(long).head + branchParts(long).tail).toBe(long)
  })

  it('a task running parallel branches shows all active step titles and responsible Agents even when single current fields are null', async () => {
    list()
    const item = await row('Parallel branches task')
    const steps = item.querySelector<HTMLElement>('[data-parallel-steps]')!
    expect(steps).not.toBeNull()
    expect(steps).toHaveTextContent('Server · Web')
    expect(steps).toHaveAttribute('title', 'Server (server) · Web (web)')
    const agents = await within(item).findByText('backend-dev · frontend-dev')
    expect(agents).toHaveAttribute('title', 'backend-dev · frontend-dev')
  })
})
