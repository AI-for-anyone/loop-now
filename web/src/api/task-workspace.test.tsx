import { act } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/server'
import { apiUrl } from '@/test/api'
import { renderHookWithQuery } from '@/test/render'
import { makeTaskDetail, makeTaskWorkspace, taskId } from '@/test/tasks'
import { ApiError, type Schemas } from './client'
import { removeTaskWorkspace, taskKeys, useRemoveTaskWorkspace } from './tasks'

const endpoint = apiUrl(`/tasks/${taskId}/workspace/remove`)
const removed = (): Schemas['ConsoleTaskDetail'] => ({ ...makeTaskDetail('p1'), workspace: makeTaskWorkspace({ state: 'removed' }) })

describe('removing a kept worktree', () => {
  it('posts only the action id and returns the task', async () => {
    const bodies: unknown[] = []
    server.use(http.post(endpoint, async ({ request }) => { bodies.push(await request.json()); return HttpResponse.json(removed()) }))
    const detail = await removeTaskWorkspace(taskId, 'action-1')
    expect(bodies).toEqual([{ actionId: 'action-1' }])
    expect(detail.workspace?.state).toBe('removed')
  })
  it('the default test server answers task_workspace_not_kept, as the server does before a worktree exists', async () => {
    await expect(removeTaskWorkspace(taskId, 'action-1')).rejects.toMatchObject({ status: 409, code: 'task_workspace_not_kept' })
  })
  it('puts the returned task into the cache', async () => {
    server.use(http.post(endpoint, () => HttpResponse.json(removed())))
    const { result, client } = renderHookWithQuery(() => useRemoveTaskWorkspace(taskId, 'p1'))
    await act(async () => { await result.current.mutateAsync('action-2') })
    expect(client.getQueryData<Schemas['ConsoleTaskDetail']>(taskKeys.detail(taskId))?.workspace?.state).toBe('removed')
  })
  it('a 409 is an ApiError the page can tell apart', async () => {
    const { result } = renderHookWithQuery(() => useRemoveTaskWorkspace(taskId, 'p1'))
    let error: unknown
    await act(async () => { await result.current.mutateAsync('action-3').catch((e: unknown) => { error = e }) })
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ code: 'task_workspace_not_kept' })
  })
})
