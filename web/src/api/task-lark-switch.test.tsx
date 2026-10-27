import { act, waitFor } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { http, HttpResponse } from 'msw'
import { server } from '@/test/server'
import { apiUrl } from '@/test/api'
import { renderHookWithQuery } from '@/test/render'
import { taskId } from '@/test/tasks'
import { larkSwitchInput, larkSwitchReceipt, switchActionId } from '@/test/lark-switch'
import { acceptTaskLarkSwitch, mergeTaskLarkSwitch, readTaskLarkSwitch, startTaskLarkSwitch, taskLarkSwitchKeys, useTaskLarkSwitch, validateTaskLarkSwitch } from './task-lark-switch'
import { ApiError, type Schemas } from './client'

const endpoint = apiUrl(`/tasks/${taskId}/lark-account-switches`)
describe('formal switch v1 HTTP contract, not App/QA', () => {
  it('posts the exact frozen two-member body once, preserves nullable actual scopes and result', async () => {
    const body = larkSwitchInput(), writes: Schemas['TaskLarkSwitchInput'][] = []
    server.use(http.post<Record<string, never>, Schemas['TaskLarkSwitchInput']>(endpoint, async ({ request }) => { writes.push(await request.json()); return HttpResponse.json(larkSwitchReceipt(), { status: 202 }) }))
    const result = await startTaskLarkSwitch(taskId, body)
    expect(writes).toEqual([body]); expect(result.target.identity.grantedScopes).toBeNull()
    expect(result.continuation).toBeNull(); expect(result.bindingRevision).toBeNull(); expect(result.dispatchStatus).toBeNull()
  })
  it('404 reads only original action, never starts or changes an ID', async () => {
    let reads = 0, posts = 0
    server.use(http.get(endpoint + '/:id', ({ params }) => { expect(params.id).toBe(switchActionId); reads++; return HttpResponse.json({ error: { code: 'not_found', message: 'Synthetic missing action' } }, { status: 404 }) }), http.post(endpoint, () => { posts++; return HttpResponse.json({}) }))
    const { result } = renderHookWithQuery(() => useTaskLarkSwitch(taskId, switchActionId))
    await waitFor(() => expect(result.current.error).toBeInstanceOf(ApiError))
    expect(result.current.error).toMatchObject({ status: 404 }); expect(reads).toBe(1); expect(posts).toBe(0)
  })
  it.each(['pending', 'handoff', 'failed'] as const)('success absorbs late %s without pretending the dispatch finished', status => {
    const success = larkSwitchReceipt('succeeded')
    expect(mergeTaskLarkSwitch(success, larkSwitchReceipt(status))).toBe(success)
    expect(success.dispatchStatus).toBe('queued')
  })
  it('handoff does not regress and ended dispatch cannot revert to queued/running', () => {
    const handoff = larkSwitchReceipt('handoff'); expect(mergeTaskLarkSwitch(handoff, larkSwitchReceipt())).toBe(handoff)
    const saved = larkSwitchReceipt('succeeded')
    if (saved.status !== 'succeeded') throw new Error('Synthetic successful branch required')
    const done = { ...saved, dispatchStatus: 'failed' as const }
    expect(mergeTaskLarkSwitch(done, larkSwitchReceipt('succeeded'))).toBe(done)
  })
  it('same action cannot mutate target/member/source or continuation lineage', () => {
    const value = larkSwitchReceipt('succeeded')
    if (value.status !== 'succeeded') throw new Error('Synthetic successful branch required')
    expect(() => mergeTaskLarkSwitch(value, { ...value, target: { ...value.target, versionId: 'different' } })).toThrow(ApiError)
    expect(() => mergeTaskLarkSwitch(value, { ...value, continuation: { nodeRunId: 'different', attempt: 3, runId: 'other' } })).toThrow(ApiError)
    expect(() => validateTaskLarkSwitch({ ...value, members: [value.members[0], value.members[0]] }, taskId, switchActionId)).toThrow(ApiError)
    expect(() => validateTaskLarkSwitch(value, taskId, switchActionId, { ...larkSwitchInput(), targetVersionId: 'different' })).toThrow(ApiError)
  })
  it('GET validates original task/action before it can update a cache', async () => {
    server.use(http.get(endpoint + '/:id', () => HttpResponse.json({ ...larkSwitchReceipt(), actionId: 'wrong' })))
    await expect(readTaskLarkSwitch(taskId, switchActionId)).rejects.toBeInstanceOf(ApiError)
  })
  it('query cache retains succeeded identity while polling actual dispatch, independently by task/action', async () => {
    server.use(http.get(endpoint + '/:id', () => HttpResponse.json(larkSwitchReceipt())))
    const { result, client } = renderHookWithQuery(() => useTaskLarkSwitch(taskId, switchActionId))
    await waitFor(() => expect(result.current.data?.status).toBe('pending'))
    await act(async () => { acceptTaskLarkSwitch(client, larkSwitchReceipt('succeeded')) })
    await act(async () => { await result.current.refetch() })
    expect(result.current.data?.status).toBe('succeeded'); expect(result.current.data?.dispatchStatus).toBe('queued')
    expect(client.getQueryData(taskLarkSwitchKeys.action('other-task', switchActionId))).toBeUndefined()
  })
})
