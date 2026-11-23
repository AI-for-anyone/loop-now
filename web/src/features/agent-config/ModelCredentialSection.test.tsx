import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { modelAuthKey } from '@/api/model-auth'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { pluginProvider, providers, renderConfig } from '@/test/config'
import { codexProvider, makeModelAccount, makeModelLogin } from '@/test/model-auth'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { ModelCredentialSection } from './ModelCredentialSection'

const area = () => screen.getByRole('region', { name: 'Model account credential' })
const codex = () => makeAgent({ scope: 'platform', projectId: null, model: { provider: 'openai-codex', modelId: 'gpt-5-codex' }, modelCredential: makeModelAccount() })
const directory = http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, codexProvider] }))
afterEach(() => vi.restoreAllMocks())

describe('Authenticating through the official provider API', () => {
  it('starting is bound to the config version; after a blocked window only the authorization is reopened; success follows the metadata the server saved', async () => {
    const agent = codex(), starts: unknown[] = []
    let state: Schemas['ModelAuthState'] = { credential: agent.modelCredential, operation: null }
    const popup = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const { user, client, router } = await renderConfig({ agent, handlers: [directory,
      http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json(state)),
      http.post(apiUrl(`/agents/${agent.id}/model-auth/operations`), async ({ request }) => {
        starts.push(await request.json()); state = { ...state, operation: makeModelLogin(agent.id) }; return HttpResponse.json(state.operation)
      }),
    ] })
    const relogin = await within(area()).findByRole('button', { name: 'Log in again' })
    await waitFor(() => expect(relogin).toBeEnabled()); await user.click(relogin)
    await within(area()).findByText('Waiting for browser authorization')
    expect(starts).toEqual([{ provider: 'openai-codex', expectedConfigVersion: 1 }])
    expect(within(area()).getByText(/fixture-old@example.test/)).toBeInTheDocument()
    expect(relogin).toBeDisabled()
    expect(within(area()).getByText(/The authorization page didn't open/)).toBeInTheDocument()
    open.mockReturnValue(popup as unknown as Window)
    await user.click(within(area()).getByRole('button', { name: 'Open authorization page' }))
    expect(popup.location.replace).toHaveBeenCalledOnce(); expect(starts).toHaveLength(1)
    expect(within(area()).queryByText('Login succeeded')).toBeNull()
    state = { credential: makeModelAccount({ accountLabel: 'fixture-new@example.test' }), operation: makeModelLogin(agent.id, { status: 'succeeded', authorizationUrl: null }) }
    await act(() => client.invalidateQueries({ queryKey: modelAuthKey(agent.id, agent.model.provider, 1) }))
    expect(await within(area()).findByText('Login succeeded')).toBeInTheDocument()
    expect(within(area()).getByText(/fixture-new@example.test/)).toBeInTheDocument()
    expect(within(area()).queryByText(/fixture-old@example.test/)).toBeNull()
    expect(router.state.location.pathname + router.state.location.search).not.toContain('authorize')
    expect(document.body.innerHTML).not.toContain('client_id=fixture-only')
    expect(JSON.stringify({ ...localStorage, ...sessionStorage })).not.toContain('client_id=fixture-only')
    expect(screen.queryByLabelText('Environment variable name')).toBeNull()
  })

  it('when the re-login request fails, the previous success is not taken as this one', async () => {
    const agent = codex()
    const { user } = await renderConfig({ agent, handlers: [directory,
      http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: agent.modelCredential, operation: makeModelLogin(agent.id, { status: 'succeeded', authorizationUrl: null }) })),
      http.post(apiUrl(`/agents/${agent.id}/model-auth/operations`), () => HttpResponse.json(errorBody('internal_error', 'The auth helper is unavailable'), { status: 503 })),
    ] })
    vi.spyOn(window, 'open').mockReturnValue(null)
    await user.click(await within(area()).findByRole('button', { name: 'Log in again' }))
    expect(await within(area()).findByText('Login failed')).toBeInTheDocument()
    expect(within(area()).queryByText('Login succeeded')).toBeNull()
    expect(within(area()).getByText(/fixture-old@example.test/)).toBeInTheDocument()
  })

  it.each(['failed', 'expired', 'cancelled'] as const)('restores a %s operation; the old account is kept and the login can be started again', async status => {
    const agent = codex()
    await renderConfig({ agent, handlers: [directory, http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: agent.modelCredential, operation: makeModelLogin(agent.id, { status, authorizationUrl: null, errorCode: status === 'failed' ? 'auth_port_busy' : null }) }))] })
    expect(await within(area()).findByRole('button', { name: 'Start login again' })).toBeEnabled()
    expect(within(area()).getByText(/fixture-old@example.test/)).toBeInTheDocument()
    expect(within(area()).getByText('Logged in', { exact: true })).toBeInTheDocument()
    if (status === 'failed') expect(within(area()).getByText(/port is in use/)).toBeInTheDocument()
  })

  it('cancelling waits for the server to confirm; a failure keeps the waiting state and the old account; a repeated cancel is not submitted', async () => {
    const agent = codex(); let cancelCalls = 0
    let finish: ((response: Response) => void) | undefined
    const { user } = await renderConfig({ agent, handlers: [directory,
      http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: agent.modelCredential, operation: makeModelLogin(agent.id) })),
      http.post(apiUrl(`/agents/${agent.id}/model-auth/operations/:operationId/cancel`), async () => { cancelCalls++; return await new Promise<Response>(resolve => { finish = resolve }) }),
    ] })
    await user.click(await within(area()).findByRole('button', { name: 'Cancel login' }))
    expect(await within(area()).findByRole('button', { name: 'Cancelling…' })).toBeDisabled()
    expect(within(area()).queryByText('Login cancelled')).toBeNull()
    await user.click(within(area()).getByRole('button', { name: 'Cancelling…' })); expect(cancelCalls).toBe(1)
    await act(async () => finish?.(HttpResponse.json(errorBody('internal_error', 'The cancellation did not finish'), { status: 500 })))
    await within(area()).findByRole('alert')
    expect(within(area()).getByText('Waiting for browser authorization')).toBeInTheDocument()
    expect(within(area()).getByText(/fixture-old@example.test/)).toBeInTheDocument()
  })

  it('a late start response after switching Agents does not navigate or leak into the new Agent; switching providers does not show the old operation', async () => {
    const a = codex(), b = codex(); b.modelCredential = null
    let finish: ((response: Response) => void) | undefined
    server.use(directory,
      http.get(apiUrl('/agents/:agentId/model-auth'), ({ params }) => HttpResponse.json({ credential: params.agentId === a.id ? a.modelCredential : null, operation: null })),
      http.post(apiUrl(`/agents/${a.id}/model-auth/operations`), () => new Promise<Response>(resolve => { finish = resolve })),
    )
    const popup = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    vi.spyOn(window, 'open').mockReturnValue(popup as unknown as Window)
    const { rerender, client } = renderWithQuery(<ModelCredentialSection key={a.id} agent={a} />)
    const user = userEvent.setup(); await user.click(await within(area()).findByRole('button', { name: 'Log in again' }))
    await waitFor(() => expect(finish).toBeDefined())
    rerender(<ModelCredentialSection key={b.id} agent={b} />)
    await within(area()).findByRole('button', { name: 'Log in to Codex' })
    await act(async () => finish?.(HttpResponse.json(makeModelLogin(a.id))))
    expect(popup.location.replace).not.toHaveBeenCalled(); expect(popup.close).toHaveBeenCalled()
    expect(within(area()).queryByText('Waiting for browser authorization')).toBeNull()
    const other = { ...b, model: { provider: 'anthropic', modelId: 'claude' } }
    await act(async () => client.setQueryData(modelAuthKey(b.id, 'anthropic', 1), { credential: null, operation: makeModelLogin(b.id) }))
    rerender(<ModelCredentialSection key={`${b.id}:anthropic`} agent={other} />)
    expect(within(area()).queryByText('Waiting for browser authorization')).toBeNull()
  })

  it('the API key is written with provider/key, with no env field or secret cache; cancelling clears it', async () => {
    const agent = makeAgent({ scope: 'platform', projectId: null }); let body: unknown = null, credential: Schemas['CredentialMeta'] | null = null
    const { user, client } = await renderConfig({ agent, handlers: [
      http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential, operation: null })),
      http.put(apiUrl(`/agents/${agent.id}/model-credential`), async ({ request }) => { body = await request.json(); credential = makeModelAccount({ provider: 'anthropic', authMethod: 'api_key', accountLabel: null }); return HttpResponse.json(credential) }),
    ] })
    await user.click(await within(area()).findByRole('button', { name: 'Enter API key' }))
    await user.type(screen.getByLabelText('API Key'), 'fixture-secret-key'); await user.click(screen.getByRole('button', { name: 'Save' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(body).toEqual({ provider: 'anthropic', key: 'fixture-secret-key', expectedConfigVersion: 1 })
    expect(JSON.stringify(client.getMutationCache().getAll().map(m => m.state))).not.toContain('fixture-secret-key')
    expect(JSON.stringify(client.getQueryCache().getAll().map(q => q.state))).not.toContain('fixture-secret-key')
    expect(document.body.innerHTML).not.toContain('fixture-secret-key')
    await user.click(within(area()).getByRole('button', { name: 'Replace API key' })); await user.type(screen.getByLabelText('API Key'), 'cancelled-key'); await user.click(screen.getByRole('button', { name: 'Cancel' }))
    await user.click(within(area()).getByRole('button', { name: 'Replace API key' })); expect(screen.getByLabelText('API Key')).toHaveValue('')
  })

  it('an Agent outside any project using a plugin provider without OAuth login: says console login is not available yet and shows no login button', async () => {
    const agent = makeAgent({ scope: 'platform', projectId: null, model: { provider: 'grok-cli', modelId: 'grok-4' }, modelCredential: null })
    await renderConfig({ agent, handlers: [http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, pluginProvider('grok-cli', { authMethods: [] })] }))] })
    expect(await within(area()).findByText('Console login isn\'t available for this provider yet.')).toBeInTheDocument()
    expect(within(area()).queryByRole('button', { name: /log ?in/i })).toBeNull()
  })
})
