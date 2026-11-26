import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { taskKeys, useTask } from '@/api/tasks'
import { apiUrl, errorBody } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { makeTaskDetail, makeTaskWorkspace, taskId } from '@/test/tasks'
import { TaskWorkspace } from './TaskWorkspace'

const projectId = 'p1'
const kept = (uncommittedFiles: number, unpushedCommits: number) => makeTaskWorkspace({ state: 'kept', kept: { uncommittedFiles, unpushedCommits } })
const show = (workspace: Schemas['TaskWorkspace'] | null) => renderWithQuery(<TaskWorkspace taskId={taskId} projectId={projectId} workspace={workspace} />)
const removeUrl = apiUrl(`/tasks/${taskId}/workspace/remove`)

describe('the worktree on the task page', () => {
  it('a task without a worktree shows nothing', () => {
    const { container } = show(null)
    expect(container).toBeEmptyDOMElement()
  })
  it('an active worktree shows repository, branch, where it started and the folder with a copy button', async () => {
    const workspace = makeTaskWorkspace()
    show(workspace)
    const block = screen.getByRole('region', { name: 'Worktree' })
    expect(within(block).getByText('in use')).toBeVisible()
    expect(within(block).getByText(workspace.repository)).toBeVisible()
    expect(within(block).getByText(workspace.branch)).toBeVisible()
    expect(within(block).getByText('from main @ 4f2c9a1')).toBeVisible()
    expect(within(block).getByText(workspace.path)).toBeVisible()
    const user = userEvent.setup()
    await user.click(within(block).getByRole('button', { name: 'Copy' }))
    expect(await navigator.clipboard.readText()).toBe(workspace.path)
    expect(within(block).queryByRole('button', { name: 'Remove worktree' })).not.toBeInTheDocument()
  })
  it('while preparing, a base that is not known yet and a missing commit are left out', () => {
    show(makeTaskWorkspace({ state: 'pending', baseBranch: '', baseCommit: null }))
    const block = screen.getByRole('region', { name: 'Worktree' })
    expect(within(block).getByText('being prepared')).toBeVisible()
    expect(within(block).queryByText(/^from /)).not.toBeInTheDocument()
  })
  it('a base branch without a commit yet reads "from main"', () => {
    show(makeTaskWorkspace({ state: 'pending', baseCommit: null }))
    expect(screen.getByText('from main')).toBeVisible()
  })
  it('a removed worktree says so and offers no copy of a folder that is gone', () => {
    show(makeTaskWorkspace({ state: 'removed' }))
    const block = screen.getByRole('region', { name: 'Worktree' })
    expect(within(block).getByText('removed')).toBeVisible()
    expect(within(block).queryByRole('button', { name: 'Copy' })).not.toBeInTheDocument()
    expect(within(block).queryByRole('button', { name: 'Remove worktree' })).not.toBeInTheDocument()
  })
  it('a kept worktree says why in numbers and offers Remove', () => {
    show(kept(3, 2))
    const block = screen.getByRole('region', { name: 'Worktree' })
    expect(within(block).getByText('kept')).toBeVisible()
    expect(within(block).getByText('Kept, because removing it would lose work:')).toBeVisible()
    expect(within(block).getByText('3 files with uncommitted changes')).toBeVisible()
    expect(within(block).getByText('2 commits that exist only here')).toBeVisible()
    expect(within(block).getByRole('button', { name: 'Remove worktree' })).toBeEnabled()
  })
  it('a zero count is not printed', () => {
    show(kept(1, 0))
    expect(screen.getByText('1 file with uncommitted changes')).toBeVisible()
    expect(screen.queryByText(/commits? that exists? only here/)).not.toBeInTheDocument()
    expect(screen.queryByText(/^0 /)).not.toBeInTheDocument()
  })
  it('kept with nothing counted still explains itself without numbers', () => {
    show(kept(0, 0))
    expect(screen.getByText('Kept. Look at the folder before removing it.')).toBeVisible()
    expect(screen.queryByText(/^0 /)).not.toBeInTheDocument()
  })
  it('Remove asks inside the page, says what is lost and what stays, and cancelling does not call', async () => {
    let calls = 0
    server.use(http.post(removeUrl, () => { calls++; return HttpResponse.json({}) }))
    show(kept(3, 2))
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Remove worktree' }))
    const dialog = screen.getByRole('alertdialog')
    expect(within(dialog).getByText('Remove this worktree?')).toBeVisible()
    expect(within(dialog).getByText('The 3 files with uncommitted changes are lost for good.')).toBeVisible()
    expect(within(dialog).getByText('The 2 commits that exist only here stay on branch task/12-general-task-acceptance, not pushed.')).toBeVisible()
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(calls).toBe(0)
  })
  it('confirming calls once with an action id, is busy while it runs, and puts the removed task in the cache', async () => {
    const bodies: unknown[] = []
    let release!: () => void
    const gate = new Promise<void>(resolve => { release = resolve })
    server.use(http.post(removeUrl, async ({ request }) => {
      bodies.push(await request.json()); await gate
      return HttpResponse.json({ ...makeTaskDetail(projectId), workspace: makeTaskWorkspace({ state: 'removed' }) })
    }))
    const { client } = show(kept(1, 0))
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Remove worktree' }))
    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Remove worktree' }))
    await waitFor(() => expect(within(dialog).getByRole('button', { name: /Remove worktree/ })).toBeDisabled())
    release()
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(bodies).toHaveLength(1)
    expect(bodies[0]).toEqual({ actionId: expect.stringMatching(/^[0-9a-f-]{36}$/) })
    expect(client.getQueryData<Schemas['ConsoleTaskDetail']>(taskKeys.detail(taskId))?.workspace?.state).toBe('removed')
  })
  it('task_workspace_not_kept closes the question and reads the task again instead of showing an error', async () => {
    let reads = 0
    server.use(
      http.post(removeUrl, () => HttpResponse.json(errorBody('task_workspace_not_kept'), { status: 409 })),
      http.get(apiUrl(`/tasks/${taskId}`), () => { reads++; return HttpResponse.json({ ...makeTaskDetail(projectId), workspace: reads === 1 ? kept(1, 0) : makeTaskWorkspace({ state: 'removed' }) }) }),
    )
    // As on the page: the block follows the task the page reads.
    function Page() {
      const task = useTask(taskId)
      return task.data ? <TaskWorkspace taskId={taskId} projectId={projectId} workspace={task.data.workspace} /> : null
    }
    renderWithQuery(<Page />)
    const user = userEvent.setup()
    await user.click(await screen.findByRole('button', { name: 'Remove worktree' }))
    await user.click(within(screen.getByRole('alertdialog')).getByRole('button', { name: 'Remove worktree' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(await screen.findByText('removed')).toBeVisible()
    expect(reads).toBe(2)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
  it('another failure stays in the question with its message', async () => {
    server.use(http.post(removeUrl, () => HttpResponse.json(errorBody('internal_error', 'git said no'), { status: 500 })))
    show(kept(1, 0))
    const user = userEvent.setup()
    await user.click(screen.getByRole('button', { name: 'Remove worktree' }))
    const dialog = screen.getByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Remove worktree' }))
    expect(await within(dialog).findByText(/git said no/)).toBeVisible()
  })
})
