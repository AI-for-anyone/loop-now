import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, errorBody } from '@/test/api'
import { server } from '@/test/server'
import { api, ApiError, call, rawBody } from './client'

describe('call', () => {
  it('成功时返回数据', async () => {
    server.use(http.get(apiUrl('/skills'), () => HttpResponse.json({ skills: [] })))
    await expect(call(api.GET('/skills'))).resolves.toEqual({ skills: [] })
  })

  it('把统一错误结构转换为 ApiError，保留字段级错误', async () => {
    server.use(
      http.post(apiUrl('/projects/p1/agents'), () =>
        HttpResponse.json(errorBody('validation_failed', '校验失败', { fields: [{ field: 'name', message: '不能为空' }] }), { status: 422 }),
      ),
    )
    const error = await call(
      api.POST('/projects/{projectId}/agents', { params: { path: { projectId: 'p1' } }, body: { name: '', model: { provider: 'p', modelId: 'm' } } }),
    ).catch((e) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 422, code: 'validation_failed', serverMessage: '校验失败' })
    expect((error as ApiError).fieldMessage('name')).toBe('不能为空')
  })

  it('agent_busy 带上当前执行与排队数量', async () => {
    const details = { currentRunId: 'run-1', queued: { interrupt: 1, normal: 2 } }
    server.use(http.post(apiUrl('/agents/a1/messages'), () => HttpResponse.json(errorBody('agent_busy', '正忙', { details }), { status: 409 })))
    const error = await call(api.POST('/agents/{agentId}/messages', { params: { path: { agentId: 'a1' } }, body: { text: 'x' } })).catch((e) => e)
    expect(error).toMatchObject({ status: 409, code: 'agent_busy', details })
  })

  it.each([
    [412, 'precondition_failed'],
    [413, 'payload_too_large'],
  ] as const)('%i 归为 %s', async (status, code) => {
    server.use(http.put(apiUrl('/agents/a1/folder/content'), () => HttpResponse.json(errorBody(code), { status })))
    const error = await call(
      api.PUT('/agents/{agentId}/folder/content', {
        params: { path: { agentId: 'a1' }, query: { path: 'a.md' } },
        ...rawBody(new Blob(['x'])),
      }),
    ).catch((e) => e)
    expect(error).toMatchObject({ status, code })
  })

  it('非契约格式的错误归为 unknown', async () => {
    server.use(http.get(apiUrl('/skills'), () => new HttpResponse('Forbidden', { status: 403 })))
    await expect(call(api.GET('/skills'))).rejects.toMatchObject({ status: 403, code: 'unknown', serverMessage: 'Forbidden' })
  })

  it('网络错误归为 unreachable', async () => {
    server.use(http.get(apiUrl('/skills'), () => HttpResponse.error()))
    await expect(call(api.GET('/skills'))).rejects.toMatchObject({ status: 0, code: 'unreachable', requestId: null })
  })

  it('错误带上响应头中的请求编号；没有这个响应头时为 null', async () => {
    const requestId = '01890a5d-ac96-774b-bcce-b302099a8057'
    server.use(
      http.get(apiUrl('/usage'), () => HttpResponse.json(errorBody('internal_error'), { status: 500, headers: { 'Loop-Now-Request-Id': requestId } })),
      http.get(apiUrl('/skills'), () => HttpResponse.json(errorBody('internal_error'), { status: 500 })),
    )
    await expect(call(api.GET('/usage'))).rejects.toMatchObject({ status: 500, code: 'internal_error', requestId })
    await expect(call(api.GET('/skills'))).rejects.toMatchObject({ requestId: null })
  })
})

describe('rawBody', () => {
  it('以原始字节发送', async () => {
    let received: { type: string | null; body: string } | null = null
    server.use(
      http.put(apiUrl('/agents/a1/folder/content'), async ({ request }) => {
        received = { type: request.headers.get('Content-Type'), body: await request.text() }
        return HttpResponse.json({ name: 'a.md', path: 'a.md', type: 'file', size: 5, modifiedAt: '2026-10-12T00:00:00Z', symlinkTarget: null })
      }),
    )
    await call(
      api.PUT('/agents/{agentId}/folder/content', {
        params: { path: { agentId: 'a1' }, query: { path: 'a.md' } },
        ...rawBody(new Blob(['hello'])),
      }),
    )
    expect(received).toEqual({ type: 'application/octet-stream', body: 'hello' })
  })
})
