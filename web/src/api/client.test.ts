import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, errorBody } from '@/test/api'
import { server } from '@/test/server'
import { api, ApiError, call, deviceAuth, rawBody, reachability, toApiError } from './client'

describe('call', () => {
  it('returns the data on success', async () => {
    server.use(http.get(apiUrl('/skills'), () => HttpResponse.json({ skills: [] })))
    await expect(call(api.GET('/skills'))).resolves.toEqual({ skills: [] })
  })

  it('turns the common error body into an ApiError, keeping field-level errors', async () => {
    server.use(
      http.post(apiUrl('/projects/p1/agents'), () =>
        HttpResponse.json(errorBody('validation_failed', 'name: Must not be empty', { fields: [{ field: 'name', message: 'Must not be empty' }] }), { status: 422 }),
      ),
    )
    const error = await call(
      api.POST('/projects/{projectId}/agents', { params: { path: { projectId: 'p1' } }, body: { name: '', model: { provider: 'p', modelId: 'm' } } }),
    ).catch((e) => e)
    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 422, code: 'validation_failed', serverMessage: 'name: Must not be empty' })
    expect((error as ApiError).fieldMessage('name')).toBe('Must not be empty')
  })

  // Every code in the contract must survive parsing; these two were once missing and fell back to 'unknown'.
  it.each(['cli_auth_in_progress', 'credential_selection_mode_conflict', 'remote_access_off', 'pairing_code_invalid', 'rate_limited'] as const)('keeps the contract code %s instead of falling back to unknown', (code) => {
    expect(toApiError(409, errorBody(code, 'conflict'))).toMatchObject({ status: 409, code, serverMessage: 'conflict' })
  })

  it('agent_busy carries the current run and the queued counts', async () => {
    const details = { currentRunId: 'run-1', queued: { interrupt: 1, normal: 2 } }
    server.use(http.post(apiUrl('/agents/a1/messages'), () => HttpResponse.json(errorBody('agent_busy', 'The Agent is replying; interrupt it or queue the message', { details }), { status: 409 })))
    const error = await call(api.POST('/agents/{agentId}/messages', { params: { path: { agentId: 'a1' } }, body: { text: 'x' } })).catch((e) => e)
    expect(error).toMatchObject({ status: 409, code: 'agent_busy', details })
  })

  it.each([
    [412, 'precondition_failed'],
    [413, 'payload_too_large'],
  ] as const)('%i maps to %s', async (status, code) => {
    server.use(http.put(apiUrl('/agents/a1/folder/content'), () => HttpResponse.json(errorBody(code), { status })))
    const error = await call(
      api.PUT('/agents/{agentId}/folder/content', {
        params: { path: { agentId: 'a1' }, query: { path: 'a.md' } },
        ...rawBody(new Blob(['x'])),
      }),
    ).catch((e) => e)
    expect(error).toMatchObject({ status, code })
  })

  it('an error outside the contract format maps to unknown', async () => {
    server.use(http.get(apiUrl('/skills'), () => new HttpResponse('Forbidden', { status: 403 })))
    await expect(call(api.GET('/skills'))).rejects.toMatchObject({ status: 403, code: 'unknown', serverMessage: 'Forbidden' })
  })

  it('a network error maps to unreachable', async () => {
    server.use(http.get(apiUrl('/skills'), () => HttpResponse.error()))
    await expect(call(api.GET('/skills'))).rejects.toMatchObject({ status: 0, code: 'unreachable', requestId: null })
  })

  it('the error carries the request ID from the response header; null without that header', async () => {
    const requestId = '01890a5d-ac96-774b-bcce-b302099a8057'
    server.use(
      http.get(apiUrl('/usage'), () => HttpResponse.json(errorBody('internal_error'), { status: 500, headers: { 'Loop-Now-Request-Id': requestId } })),
      http.get(apiUrl('/skills'), () => HttpResponse.json(errorBody('internal_error'), { status: 500 })),
    )
    await expect(call(api.GET('/usage'))).rejects.toMatchObject({ status: 500, code: 'internal_error', requestId })
    await expect(call(api.GET('/skills'))).rejects.toMatchObject({ requestId: null })
  })
})

describe('reachability', () => {
  // The test decides when the response to a request is returned: when fail is true, the request ends with a network error.
  function heldHandler(path: string) {
    let release: (fail: boolean) => void = () => {}
    const held = new Promise<boolean>((resolve) => (release = resolve))
    server.use(http.get(apiUrl(path), async () => ((await held) ? HttpResponse.error() : HttpResponse.json({ skills: [], projects: [] }))))
    return (fail: boolean) => release(fail)
  }

  it('unreachable when a request does not reach the server; back as soon as any later request gets a response', async () => {
    server.use(http.get(apiUrl('/skills'), () => HttpResponse.error()), http.get(apiUrl('/projects'), () => HttpResponse.json({ projects: [] })))
    await api.GET('/skills').catch(() => {})
    expect(reachability.isReachable()).toBe(false)
    await api.GET('/projects')
    expect(reachability.isReachable()).toBe(true)
  })

  it('a request sent while disconnected that fails only after recovery is stale and does not turn back to unreachable', async () => {
    const releaseOld = heldHandler('/skills')
    const old = api.GET('/skills').catch(() => {})
    server.use(http.get(apiUrl('/projects'), () => HttpResponse.json({ projects: [] })))
    await api.GET('/projects')
    expect(reachability.isReachable()).toBe(true)
    releaseOld(true)
    await old
    expect(reachability.isReachable()).toBe(true)
  })

  it('an earlier request that succeeds after a newer one failed is stale too: still unreachable', async () => {
    const releaseOld = heldHandler('/skills')
    const old = api.GET('/skills')
    server.use(http.get(apiUrl('/projects'), () => HttpResponse.error()))
    await api.GET('/projects').catch(() => {})
    expect(reachability.isReachable()).toBe(false)
    releaseOld(false)
    await old
    expect(reachability.isReachable()).toBe(false)
  })

  it('a request aborted after it reached the server does not change reachability', async () => {
    let arrive: () => void = () => {}
    const arrived = new Promise<void>((resolve) => (arrive = resolve))
    const releaseOld = heldHandler('/skills')
    server.use(http.get(apiUrl('/skills'), () => arrive()))
    const controller = new AbortController()
    const pending = api.GET('/skills', { signal: controller.signal })
    await arrived
    controller.abort()
    await expect(pending).rejects.toMatchObject({ name: 'AbortError' })
    expect(reachability.isReachable()).toBe(true)
    releaseOld(false)
  })
})

describe('rawBody', () => {
  it('sends the raw bytes', async () => {
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

describe('deviceAuth', () => {
  it('marks unauthenticated when /context returns 401 before any door is known', async () => {
    server.use(http.get(apiUrl('/context'), () => HttpResponse.json(errorBody('unauthenticated'), { status: 401 })))
    await api.GET('/context')
    expect(deviceAuth.isUnauthenticated()).toBe(true)
  })

  it('marks unauthenticated on any 401 once the remote door is known, and ignores stale 401s after pairing', async () => {
    deviceAuth.noteDoor('remote')
    let releaseStale: () => void = () => {}
    const staleHeld = new Promise<void>((resolve) => {
      releaseStale = resolve
    })
    server.use(
      http.get(apiUrl('/projects'), async () => {
        await staleHeld
        return HttpResponse.json(errorBody('unauthenticated'), { status: 401 })
      }),
      http.get(apiUrl('/skills'), () => HttpResponse.json(errorBody('unauthenticated'), { status: 401 })),
    )

    const stale = api.GET('/projects')
    await api.GET('/skills')
    expect(deviceAuth.isUnauthenticated()).toBe(true)

    deviceAuth.markAuthenticated()
    expect(deviceAuth.isUnauthenticated()).toBe(false)

    releaseStale()
    await stale
    expect(deviceAuth.isUnauthenticated()).toBe(false)
  })

  it('does not lock out the local door on an isolated 401', async () => {
    deviceAuth.noteDoor('local')
    server.use(http.get(apiUrl('/skills'), () => HttpResponse.json(errorBody('unauthenticated'), { status: 401 })))
    await api.GET('/skills')
    expect(deviceAuth.isUnauthenticated()).toBe(false)
  })
})
