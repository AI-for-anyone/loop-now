import { act, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { modelAuthKey } from '@/api/model-auth'
import { apiUrl, errorBody, makeAgent } from '@/test/api'
import { pluginProvider, providers, renderConfig } from '@/test/config'
import { makeModelAccount, makeModelLogin } from '@/test/model-auth'

// A platform Agent (not part of a project) logs in, within its own model credentials, to a provider registered
// by a plugin: browser authorization, device code, questions asked mid-login, and the local-login notes.
const area = () => screen.getByRole('region', { name: 'Model account credential' })
const grokAgent = () => makeAgent({ scope: 'platform', projectId: null, model: { provider: 'grok-cli', modelId: 'grok-4' }, modelCredential: null })
const grokLogin = (agentId: string, overrides: Partial<Schemas['ModelAuthOperation']> = {}) =>
  makeModelLogin(agentId, { id: '00000000-0000-7000-8000-0000000003a1', provider: 'grok-cli', status: 'pending', authorizationUrl: null, ...overrides })
const catalog = http.get(apiUrl('/model-providers'), () => HttpResponse.json({ ...providers, providers: [...providers.providers, pluginProvider('grok-cli')] }))
const grokModels = http.get(apiUrl('/model-providers/:providerId/models'), ({ params }) =>
  HttpResponse.json({ providerId: params.providerId, models: [{ id: 'grok-4', name: 'Grok 4', reasoning: false, contextWindow: 1, maxTokens: 1, input: ['text'] }] }))

afterEach(() => vi.restoreAllMocks())

// The test controls the login state: starting returns start, and every later read returns box.state (answering and cancelling rewrite it).
async function setup(start: Schemas['ModelAuthOperation'] | ((agent: Schemas['Agent']) => Response), options: { state?: Partial<Schemas['ModelAuthState']>; answer?: (body: Schemas['ProjectModelLoginInput']) => Response | undefined } = {}) {
  const agent = grokAgent()
  const starts: unknown[] = [], answers: Schemas['ProjectModelLoginInput'][] = []
  const box: { state: Schemas['ModelAuthState'] } = { state: { credential: null, operation: null, ...options.state } }
  const open = vi.spyOn(window, 'open').mockReturnValue(null)
  const rendered = await renderConfig({ agent, handlers: [catalog, grokModels,
    http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json(box.state)),
    http.post(apiUrl(`/agents/${agent.id}/model-auth/operations`), async ({ request }) => {
      starts.push(await request.json())
      if (typeof start === 'function') return start(agent)
      const operation = { ...start, agentId: agent.id }
      box.state = { ...box.state, operation }
      return HttpResponse.json(operation, { status: 202 })
    }),
    http.post(apiUrl(`/agents/${agent.id}/model-auth/operations/:operationId/input`), async ({ request }) => {
      const body = (await request.json()) as Schemas['ProjectModelLoginInput']
      answers.push(body)
      const custom = options.answer?.(body)
      if (custom) return custom
      box.state = { ...box.state, operation: { ...box.state.operation!, prompt: null } }
      return HttpResponse.json(box.state)
    }),
    http.post(apiUrl(`/agents/${agent.id}/model-auth/operations/:operationId/cancel`), () => {
      box.state = { ...box.state, operation: { ...box.state.operation!, status: 'cancelled', authorizationUrl: null, deviceCode: null, prompt: null } }
      return HttpResponse.json(box.state.operation)
    }),
  ] })
  const login = async () => {
    const button = await within(area()).findByRole('button', { name: 'Log in to Grok CLI' })
    await waitFor(() => expect(button).toBeEnabled())
    await rendered.user.click(button)
  }
  return { ...rendered, agent, starts, answers, open, box, login }
}

describe('A platform Agent logs in to a plugin provider', () => {
  it('browser authorization: starts for the current provider without opening a blank window first; a URL that passes the check is given as a native link naming the target host; cancelling waits for the server to confirm', async () => {
    const url = 'https://accounts.x.ai/oauth2/auth?client_id=grok&state=s'
    const { starts, open, login, user } = await setup(grokLogin('', { status: 'awaiting_authorization', authorizationUrl: url }))
    expect(within(area()).queryByText(/can't sign in here/)).toBeNull()
    await login()
    expect(starts).toEqual([{ provider: 'grok-cli', expectedConfigVersion: 1 }])
    const link = await within(area()).findByRole('link', { name: 'Open authorization page (accounts.x.ai)' })
    expect(link).toHaveAttribute('href', url)
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(open).not.toHaveBeenCalled()
    expect(within(area()).getByRole('button', { name: 'Log in to Grok CLI' })).toBeDisabled()
    await user.click(within(area()).getByRole('button', { name: 'Cancel login' }))
    expect(await within(area()).findByText('Login cancelled')).toBeInTheDocument()
    expect(within(area()).queryByRole('link', { name: /Open authorization page/ })).toBeNull()
  })

  it('an authorization URL that fails the generic check: no link, the reason is shown and the status can be read again', async () => {
    const { login } = await setup(grokLogin('', { status: 'awaiting_authorization', authorizationUrl: 'http://accounts.x.ai/oauth2/auth?state=s' }))
    await login()
    expect(await within(area()).findByText('The authorization URL failed the safety check; the unknown URL wasn\'t opened and the login won\'t restart automatically.')).toBeInTheDocument()
    expect(within(area()).queryByRole('link', { name: /Open authorization page/ })).toBeNull()
    expect(within(area()).getByRole('button', { name: 'Read the login status again' })).toBeEnabled()
  })

  it('device code: shows the code large, links to the page for entering it and states the expiry', async () => {
    const { login } = await setup(grokLogin('', { status: 'awaiting_authorization', deviceCode: { userCode: 'GROK-1234', verificationUri: 'https://accounts.x.ai/device', expiresAt: '2099-10-14T09:00:00Z' } }))
    await login()
    expect(await within(area()).findByText('GROK-1234')).toBeInTheDocument()
    expect(within(area()).getByText('Open accounts.x.ai and enter the code below to finish logging in.')).toBeInTheDocument()
    expect(within(area()).getByRole('link', { name: 'Open accounts.x.ai' })).toHaveAttribute('href', 'https://accounts.x.ai/device')
    expect(within(area()).getByText(/Code valid until/)).toBeInTheDocument()
    expect(within(area()).getByRole('button', { name: 'Cancel login' })).toBeEnabled()
  })

  it('multiple choice: the extension text is shown as plain text with nothing preselected; the question goes away after the chosen id is submitted', async () => {
    const prompt: Schemas['ProjectModelLoginPrompt'] = { id: 'p1', kind: 'select', message: '<b>Choose an account type</b>', options: [{ id: 'personal', label: 'Individual' }, { id: 'team', label: 'Team <i>' }] }
    const { login, user, answers } = await setup(grokLogin('', { prompt }))
    await login()
    expect(await within(area()).findByText('The login needs your answer')).toBeInTheDocument()
    expect(within(area()).getByText('<b>Choose an account type</b>')).toBeInTheDocument()
    expect(within(area()).getByRole('radio', { name: 'Individual' })).not.toBeChecked()
    expect(within(area()).getByRole('radio', { name: 'Team <i>' })).not.toBeChecked()
    await user.click(within(area()).getByRole('button', { name: 'Submit' }))
    expect(within(area()).getByText('Choose one')).toBeInTheDocument()
    expect(answers).toEqual([])
    await user.click(within(area()).getByRole('radio', { name: 'Team <i>' }))
    await user.click(within(area()).getByRole('button', { name: 'Submit' }))
    await waitFor(() => expect(answers).toEqual([{ promptId: 'p1', value: 'team' }]))
    await waitFor(() => expect(within(area()).queryByRole('radio')).toBeNull())
  })

  it('manual entry: the input clears after submitting and the answer is neither cached nor left on the page; a 422 is explained and can be answered again', async () => {
    const prompt: Schemas['ProjectModelLoginPrompt'] = { id: 'p2', kind: 'manual_code', message: '', options: [] }
    let reject = true
    const { login, user, answers, client } = await setup(grokLogin('', { prompt }), {
      answer: () => {
        if (!reject) return undefined
        reject = false
        return HttpResponse.json(errorBody('validation_failed', 'value: Invalid authorization code', { fields: [{ field: 'value', message: 'Invalid authorization code' }] }), { status: 422 })
      },
    })
    await login()
    const input = await within(area()).findByLabelText('If the browser can\'t reach the callback, paste the callback URL from the address bar, or the code shown on the page, here.')
    await user.type(input, 'fixture-secret-code')
    await user.click(within(area()).getByRole('button', { name: 'Submit' }))
    expect(await within(area()).findByText(/Invalid authorization code/)).toBeInTheDocument()
    expect(input).toHaveValue('')
    await user.type(input, 'fixture-second-code')
    await user.click(within(area()).getByRole('button', { name: 'Submit' }))
    await waitFor(() => expect(within(area()).queryByLabelText(/If the browser can't reach the callback/)).toBeNull())
    expect(answers.map(answer => answer.value)).toEqual(['fixture-secret-code', 'fixture-second-code'])
    const caches = JSON.stringify([client.getQueryCache().getAll().map(q => q.state), client.getMutationCache().getAll().map(m => m.state), { ...localStorage, ...sessionStorage }])
    expect(caches).not.toContain('fixture-secret-code')
    expect(caches).not.toContain('fixture-second-code')
    expect(document.body.innerHTML).not.toContain('fixture-second-code')
  })

  it('text question: uses the default label when there is no message; submitting it empty only shows a hint', async () => {
    const { login, user, answers } = await setup(grokLogin('', { prompt: { id: 'p3', kind: 'text', message: '  ', options: [] } }))
    await login()
    await within(area()).findByLabelText('Answer')
    await user.click(within(area()).getByRole('button', { name: 'Submit' }))
    expect(within(area()).getByText('Enter an answer')).toBeInTheDocument()
    expect(answers).toEqual([])
  })

  it('success: follows the account the server saved; the login button becomes "Log in again"', async () => {
    const { login, box, client, agent } = await setup(grokLogin('', { status: 'awaiting_authorization', authorizationUrl: 'https://accounts.x.ai/oauth2/auth?state=s' }))
    await login()
    await within(area()).findByRole('link', { name: 'Open authorization page (accounts.x.ai)' })
    box.state = { credential: makeModelAccount({ provider: 'grok-cli', accountLabel: null }), operation: grokLogin(agent.id, { status: 'succeeded', completedAt: '2026-10-14T08:05:00Z' }) }
    await act(() => client.invalidateQueries({ queryKey: modelAuthKey(agent.id, 'grok-cli', 1) }))
    expect(await within(area()).findByText('Login succeeded')).toBeInTheDocument()
    expect(within(area()).getByText('Logged in', { exact: true })).toBeInTheDocument()
    expect(within(area()).getByRole('button', { name: 'Log in again' })).toBeEnabled()
  })

  it.each([
    ['plugin_unavailable', 'The plugin was uninstalled or turned off, so this login did not complete.'],
    ['extension_load_failed', 'The plugin\'s extension failed to load or doesn\'t register a login for this provider, so this login did not complete.'],
  ] as const)('failure (%s): states the reason and the login can be started again', async (errorCode, text) => {
    const agent = grokAgent()
    await renderConfig({ agent, handlers: [catalog, grokModels, http.get(apiUrl(`/agents/${agent.id}/model-auth`), () => HttpResponse.json({ credential: null, operation: grokLogin(agent.id, { status: 'failed', errorCode, completedAt: '2026-10-14T08:05:00Z' }) }))] })
    expect(await within(area()).findByText(text)).toBeInTheDocument()
    expect(within(area()).getByText('Login failed')).toBeInTheDocument()
    expect(within(area()).getByRole('button', { name: 'Start login again' })).toBeEnabled()
  })

  it('409 plugin_not_enabled: says the plugin is not granted to this Agent yet, not that it is "not enabled"', async () => {
    const { login } = await setup(() => HttpResponse.json(errorBody('plugin_not_enabled', 'Plugin grok is not granted to this Agent'), { status: 409 }))
    await login()
    expect(await within(area()).findByText('The plugin "Grok subscription" isn\'t granted to this Agent yet, so no login was started. Grant it under "Plugin grants" on this page, then log in.')).toBeInTheDocument()
    expect(within(area()).queryByText(/The plugin isn't enabled/)).toBeNull()
    expect(within(area()).getByText('Login failed')).toBeInTheDocument()
  })

  it('with "Use local logins" on: active says the local login is used; missing warns prominently that starting will fail and links to the settings', async () => {
    const active = await setup(grokLogin(''), { state: { credential: makeModelAccount({ provider: 'grok-cli', accountLabel: null }), localLogin: 'active' } })
    expect(await within(area()).findByText('"Use local logins" is on: this uses the extension\'s own login on this computer, not this Agent\'s login.')).toHaveAttribute('role', 'note')
    expect(within(area()).queryByRole('alert')).toBeNull()
    active.unmount()
    await setup(grokLogin(''), { state: { localLogin: 'missing' } })
    const missing = await within(area()).findByRole('alert')
    expect(missing).toHaveTextContent('"Use local logins" is on, but this computer has no login for the extension, so the Agent will fail to start. Turn the switch off, or log in on this computer, and try again.')
    expect(within(missing).getByRole('link', { name: 'Open local mode settings' })).toHaveAttribute('href', '/settings/workspace#local-mode')
  })
})
