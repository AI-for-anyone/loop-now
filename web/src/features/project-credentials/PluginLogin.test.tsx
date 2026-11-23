import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, defaultProject, errorBody } from '@/test/api'
import { pluginProvider } from '@/test/config'
import { projectAccount, projectOperation } from '@/test/project-credentials'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { ProjectAccounts } from './ProjectAccounts'
import { ProjectLogin } from './ProjectLogin'

// Project login for a provider registered by a plugin: browser authorization, device code, and questions asked mid-login.
const grok = pluginProvider('grok-cli')
const grokOp = (overrides: Partial<Schemas['ProjectModelAuthOperation']> = {}) =>
  projectOperation({ id: '30000000-0000-7000-8000-0000000000a1', provider: 'grok-cli', name: 'Grok account', status: 'pending', authorizationUrl: null, ...overrides })

afterEach(() => vi.restoreAllMocks())

// The test controls the login state: starting returns start, and every later read returns state (answering and cancelling rewrite it).
function setup(start: Schemas['ProjectModelAuthOperation'], options: { account?: Schemas['ProjectCredentialAccount']; list?: Schemas['ProjectModelAuthOperation'][]; answer?: (body: Schemas['ProjectModelLoginInput']) => Response | undefined } = {}) {
  const starts: unknown[] = [], answers: Schemas['ProjectModelLoginInput'][] = []
  const box: { state: Schemas['ProjectModelAuthState'] | null } = { state: null }
  const open = vi.spyOn(window, 'open').mockReturnValue(null)
  server.use(
    http.get(apiUrl('/projects/:projectId/model-auth/operations'), ({ request }) => {
      expect(new URL(request.url).searchParams.has('provider')).toBe(false)
      return HttpResponse.json({ operations: options.list ?? [] })
    }),
    http.post(apiUrl('/projects/:projectId/model-auth/operations'), async ({ request }) => {
      starts.push(await request.json())
      box.state = { operation: start, account: null }
      return HttpResponse.json(box.state, { status: 202 })
    }),
    http.get(apiUrl('/projects/:projectId/model-auth/operations/:operationId'), () => HttpResponse.json(box.state)),
    http.post(apiUrl('/projects/:projectId/model-auth/operations/:operationId/input'), async ({ request }) => {
      const body = (await request.json()) as Schemas['ProjectModelLoginInput']
      answers.push(body)
      const custom = options.answer?.(body)
      if (custom) return custom
      box.state = { operation: { ...box.state!.operation, prompt: null }, account: null }
      return HttpResponse.json(box.state)
    }),
  )
  const user = userEvent.setup()
  const result = renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} provider={grok} account={options.account} /></DraftNavigationGuard> }], '/')
  const begin = async () => {
    await user.type(screen.getByLabelText('New Grok CLI account name'), 'Grok account')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Log in to Grok CLI' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: 'Log in to Grok CLI' }))
  }
  return { ...result, user, starts, answers, open, box, begin }
}

describe('Project login for plugin providers', () => {
  it('browser authorization: starts by provider id without preopening a blank window; once the URL passes the generic check it offers a native link that names the target host', async () => {
    const op = grokOp({ status: 'awaiting_authorization', authorizationUrl: 'https://accounts.x.ai/oauth2/auth?client_id=grok&state=s' })
    const { starts, open, begin } = setup(op)
    await begin()
    expect(await screen.findByText('Waiting for browser authorization')).toBeInTheDocument()
    expect(starts).toEqual([{ provider: 'grok-cli', name: 'Grok account' }])
    expect(open).not.toHaveBeenCalled()
    const link = screen.getByRole('link', { name: 'Open authorization page (accounts.x.ai)' })
    expect(link).toHaveAttribute('href', op.authorizationUrl)
    expect(link).toHaveAttribute('rel', 'noopener noreferrer')
    expect(screen.getByLabelText('New Grok CLI account name')).toHaveAccessibleDescription(/Open authorization page/)
  })

  it('when the URL fails the generic check (http, a user name, a fragment) there is no link, and it says the unknown URL was not opened', async () => {
    for (const authorizationUrl of ['http://accounts.x.ai/auth', 'https://user:pw@accounts.x.ai/auth', 'https://accounts.x.ai/auth#token']) {
      const { begin, unmount } = setup(grokOp({ status: 'awaiting_authorization', authorizationUrl }))
      await begin()
      expect(await screen.findByText(/the unknown URL wasn't opened and the login won't restart automatically/)).toBeInTheDocument()
      expect(screen.queryByRole('link', { name: /Open authorization page/ })).toBeNull()
      unmount()
    }
  })

  it('device code: shows the code in large type and copyable; links to the code-entry URL naming its host; states when the code expires', async () => {
    const { begin, user } = setup(grokOp({ status: 'awaiting_authorization', deviceCode: { userCode: 'ABCD-1234', verificationUri: 'https://x.ai/device', expiresAt: '2026-10-15T01:10:00Z' } }))
    await begin()
    expect(await screen.findByText('Waiting for browser authorization')).toBeInTheDocument()
    expect(screen.getByText('Open x.ai and enter the code below to finish logging in.')).toBeInTheDocument()
    expect(screen.getByText('ABCD-1234')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open x.ai' })).toHaveAttribute('href', 'https://x.ai/device')
    expect(screen.getByText(/^Code valid until /)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Copy' }))
    expect(await navigator.clipboard.readText()).toBe('ABCD-1234')
    expect(screen.getByRole('button', { name: 'Copied' })).toBeInTheDocument()
    expect(screen.getByLabelText('New Grok CLI account name')).toHaveAccessibleDescription(/Follow the steps below to continue/)
  })

  it('asking mid-way to choose an option: nothing is preselected, a hint shows when nothing is chosen; after submitting the option id it clears, and the answer goes into no cache', async () => {
    const prompt: Schemas['ProjectModelLoginPrompt'] = { id: 'p1', kind: 'select', message: 'How do you want to log in?', options: [{ id: 'browser', label: 'Browser (this computer)' }, { id: 'device', label: '<b>Device code</b>' }] }
    const { begin, user, answers, client } = setup(grokOp({ prompt }))
    await begin()
    expect(await screen.findByText('The login needs your answer')).toBeInTheDocument()
    const group = screen.getByRole('group', { name: 'How do you want to log in?' })
    const radios = within(group).getAllByRole('radio')
    expect(radios.map((radio) => (radio as HTMLInputElement).checked)).toEqual([false, false])
    expect(within(group).getByText('<b>Device code</b>')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Submit' }))
    expect(screen.getByText('Choose one')).toBeInTheDocument()
    expect(answers).toEqual([])
    await user.click(within(group).getByRole('radio', { name: '<b>Device code</b>' }))
    await user.click(screen.getByRole('button', { name: 'Submit' }))
    await waitFor(() => expect(answers).toEqual([{ promptId: 'p1', value: 'device' }]))
    await waitFor(() => expect(screen.queryByText('The login needs your answer')).toBeNull())
    expect(screen.queryByRole('group', { name: 'How do you want to log in?' })).toBeNull()
    expect(client.getMutationCache().getAll()).toEqual([])
  })

  it('pasting the callback URL (manual_code, no text from the extension): uses the default text; clears after submitting; shows why when the answer is wrong, and the answer goes into no query cache', async () => {
    const prompt: Schemas['ProjectModelLoginPrompt'] = { id: 'p2', kind: 'manual_code', message: '', options: [] }
    let reject = true
    const { begin, user, answers, client } = setup(grokOp({ prompt }), {
      answer: () => (reject ? HttpResponse.json(errorBody('validation_failed', 'value: Invalid callback URL', { fields: [{ field: 'value', message: 'Invalid callback URL' }] }), { status: 422 }) : undefined),
    })
    await begin()
    const input = await screen.findByLabelText('If the browser can\'t reach the callback, paste the callback URL from the address bar, or the code shown on the page, here.')
    await user.click(screen.getByRole('button', { name: 'Submit' }))
    expect(screen.getByText('Enter an answer')).toBeInTheDocument()
    await user.type(input, 'http://localhost:56122/callback?code=secret-code-1')
    await user.click(screen.getByRole('button', { name: 'Submit' }))
    expect(await screen.findByText(/Invalid callback URL/)).toBeInTheDocument()
    expect(input).toHaveValue('')
    reject = false
    await user.type(input, 'http://localhost:56122/callback?code=secret-code-2')
    await user.click(screen.getByRole('button', { name: 'Submit' }))
    await waitFor(() => expect(answers.map((answer) => answer.value)).toEqual(['http://localhost:56122/callback?code=secret-code-1', 'http://localhost:56122/callback?code=secret-code-2']))
    await waitFor(() => expect(screen.queryByText('The login needs your answer')).toBeNull())
    const cached = JSON.stringify(client.getQueryCache().getAll().map((query) => query.state.data))
    expect(cached).not.toContain('secret-code')
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.variables))).not.toContain('secret-code')
    expect(document.body.innerHTML).not.toContain('secret-code')
  })

  it('a text question shows the extension text as plain text, and the answer is submitted as typed', async () => {
    const prompt: Schemas['ProjectModelLoginPrompt'] = { id: 'p3', kind: 'text', message: 'Enter the organization name <org>', options: [] }
    const { begin, user, answers } = setup(grokOp({ prompt }))
    await begin()
    await user.type(await screen.findByLabelText('Enter the organization name <org>'), 'acme')
    await user.click(screen.getByRole('button', { name: 'Submit' }))
    await waitFor(() => expect(answers).toEqual([{ promptId: 'p3', value: 'acme' }]))
  })

  it('an active login is matched by provider and target account together: a new login for another provider does not lock this one', async () => {
    const codexActive = projectOperation({ name: 'Codex in progress' })
    const grokActive = grokOp({ id: '30000000-0000-7000-8000-0000000000a2', name: 'Grok in progress', status: 'awaiting_authorization', authorizationUrl: 'https://accounts.x.ai/auth' })
    const { unmount } = setup(grokActive, { list: [codexActive] })
    await waitFor(() => expect(screen.getByRole('button', { name: 'Log in to Grok CLI' })).toBeDisabled())
    await new Promise((resolve) => setTimeout(resolve, 20))
    expect(screen.getByLabelText('New Grok CLI account name')).toBeEnabled()
    expect(screen.queryByText('Waiting for browser authorization')).toBeNull()
    unmount()
    setup(grokActive, { list: [codexActive, grokActive] })
    await waitFor(() => expect(screen.getByLabelText('New Grok CLI account name')).toHaveValue('Grok in progress'))
    expect(screen.getByLabelText('New Grok CLI account name')).toBeDisabled()
  })

  it('a failure explains the new error code; when the plugin is not enabled in the project, starting is rejected with an explanation', async () => {
    const { begin, unmount } = setup(grokOp({ status: 'failed', errorCode: 'extension_load_failed' }))
    await begin()
    expect(await screen.findByText('The plugin\'s extension failed to load or doesn\'t register a login for this provider, so this login did not complete.')).toBeInTheDocument()
    unmount()
    server.use(http.post(apiUrl('/projects/:projectId/model-auth/operations'), () => HttpResponse.json(errorBody('plugin_not_enabled', 'Plugin grok is not enabled in this project'), { status: 409 })))
    renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectLogin projectId={defaultProject.id} provider={grok} /></DraftNavigationGuard> }], '/')
    const user = userEvent.setup()
    await user.type(screen.getByLabelText('New Grok CLI account name'), 'Grok account')
    await waitFor(() => expect(screen.getByRole('button', { name: 'Log in to Grok CLI' })).toBeEnabled())
    await user.click(screen.getByRole('button', { name: 'Log in to Grok CLI' }))
    expect(await screen.findByText(/^The plugin isn't enabled/)).toBeInTheDocument()
  })
})

describe('Plugin providers on the accounts page', () => {
  it('choosing a plugin provider goes straight to login; a logged-in plugin account row shows the extension name and can log in again (with account and revision)', async () => {
    const account = projectAccount({ id: '10000000-0000-7000-8000-0000000000b1', provider: 'grok-cli', authMethod: 'oauth', status: 'logged_in', name: 'My Grok', revision: 4 })
    const starts: unknown[] = []
    vi.spyOn(window, 'open').mockReturnValue(null)
    server.use(
      http.get(apiUrl('/model-providers'), () => HttpResponse.json({ piVersion: '1.4.0', catalogGeneratedAt: null, providers: [grok] })),
      http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [] })),
      http.get(apiUrl('/projects/:projectId/credential-accounts'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accounts: [account] })),
      http.get(apiUrl('/projects/:projectId/credential-account-impact'), () => HttpResponse.json({ projectId: defaultProject.id, credentialsRevision: 1, accountId: null, agents: [] })),
      http.get(apiUrl('/projects/:projectId/model-auth/operations'), () => HttpResponse.json({ operations: [] })),
      http.post(apiUrl('/projects/:projectId/model-auth/operations'), async ({ request }) => {
        starts.push(await request.json())
        return HttpResponse.json({ operation: grokOp({ targetAccountId: account.id, targetRevision: 4, status: 'failed', errorCode: 'auth_failed' }), account }, { status: 202 })
      }),
    )
    const user = userEvent.setup()
    renderRoutes([{ path: '/', element: <DraftNavigationGuard><ProjectAccounts projectId={defaultProject.id} purpose="model" /></DraftNavigationGuard> }], '/')
    const row = (await screen.findByRole('heading', { name: 'My Grok' })).closest('li')!
    await waitFor(() => expect(row).toHaveTextContent('Grok CLIgrok-cli'))
    await user.click(within(row).getByRole('button', { name: 'Log in again' }))
    await waitFor(() => expect(starts).toEqual([{ provider: 'grok-cli', accountId: account.id, expectedRevision: 4 }]))
    await user.click(screen.getByRole('button', { name: 'Add model account · provider' }))
    await user.click(await screen.findByRole('option', { name: /Grok CLI/ }))
    expect(await screen.findByLabelText('New Grok CLI account name')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Log in to Grok CLI' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Enter API key' })).toBeNull()
  })
})
