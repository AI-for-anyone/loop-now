import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { verificationKeys } from '@/api/cli-verification'
import { apiUrl, errorBody, unknownEndpointBody } from '@/test/api'
import { verificationAccount, verificationOperation, verificationPassed } from '@/test/cli-verification'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { CLIConnectionVerification } from './CLIConnectionVerification'
import { useCliTemplates } from '@/api/queries'
import { CliToolStatus } from '@/features/agent-config/CredentialStatus'
import { ProjectAccounts } from './ProjectAccounts'
import { DirtyContext } from '@/features/prompts/draft-navigation'
import { t } from '@/i18n'

const account = verificationAccount()
const base = `/projects/${account.projectId}/credential-accounts/${account.id}/verification-operations`
function setup(initial: Schemas['CLIVerificationOperation'] | null = null) {
  let op = initial
  const posts: unknown[] = [], reads: string[] = []
  server.use(http.get(apiUrl(base), () => HttpResponse.json({ accountId: account.id, currentVersionId: account.currentVersionId, operation: op })),
    http.get(apiUrl(`${base}/:id`), ({ params }) => { reads.push(String(params.id)); return op ? HttpResponse.json(op) : HttpResponse.json(errorBody('not_found'), { status: 404 }) }),
    http.post(apiUrl(base), async ({ request }) => { const body = await request.json() as Schemas['CLIVerificationStart']; posts.push(body); op = verificationOperation({ id: body.operationId }); return HttpResponse.json(op, { status: 202 }) }))
  return { posts, reads, set: (next: typeof op) => { op = next } }
}
async function readReady() { await screen.findByText(t.credentials.verification.empty) }

describe('Manual verification for five tools, formal HTTP fixture', () => {
  it.each(['mysql', 'psql', 'gh', 'gcloud', 'lark-cli'])('%s: reading and renaming do not POST; only the saved version is tested', async cli => {
    const env = setup(), view = renderWithQuery(<CLIConnectionVerification account={{ ...account, cli }} />)
    await readReady(); expect(env.posts).toHaveLength(0)
    view.rerender(<CLIConnectionVerification account={{ ...account, cli, name: 'New note', revision: 2 }} />)
    expect(screen.getByRole('button', { name: t.credentials.verification.test })).toBeEnabled()
    expect(env.posts).toHaveLength(0); expect(screen.getByText(t.credentials.verification.scope)).toBeInTheDocument()
  })
  it('only an explicit click POSTs, once, with the pinned version and no draft, command or secret', async () => {
    const user = userEvent.setup(), env = setup(); renderWithQuery(<CLIConnectionVerification account={account} />)
    await readReady(); await user.click(screen.getByRole('button', { name: t.credentials.verification.test }))
    await screen.findByText(t.credentials.verification.phase.preparing_tool)
    expect(env.posts).toHaveLength(1); expect(env.posts[0]).toEqual({ operationId: expect.any(String), expectedVersionId: account.currentVersionId })
    expect(screen.getByRole('button', { name: t.credentials.verification.again })).toBeDisabled()
  })
  it.each([
    ['mysql', 'database_select1'], ['psql', 'database_select1'], ['gh', 'github_user'], ['gcloud', 'google_crm_read'], ['lark-cli', 'lark_bot'],
  ] as const)('%s passing reports only the fixed depth and the verification time', async (cli, probeKind) => {
    setup(verificationPassed({ cli, probeKind })); renderWithQuery(<CLIConnectionVerification account={{ ...account, cli }} />)
    await screen.findByText(t.credentials.verification.status.succeeded)
    expect(screen.getByText(t.credentials.verification.depth[probeKind])).toBeInTheDocument()
    expect(screen.getByText(`${t.credentials.verification.time}2026-10-16T10:00:10Z`)).toBeInTheDocument()
  })
  it('an unknown POST result only GETs the original ID; a new attempt starts only on an explicit retry after the terminal state', async () => {
    const user = userEvent.setup(), env = setup(); let id = '', attempts = 0
    server.use(http.post(apiUrl(base), async ({ request }) => { attempts++; id = (await request.json() as Schemas['CLIVerificationStart']).operationId; env.set(verificationPassed({ id })); return HttpResponse.json(errorBody('internal_error'), { status: 502 }) }))
    renderWithQuery(<CLIConnectionVerification account={account} />); await readReady()
    await user.click(screen.getByRole('button', { name: t.credentials.verification.test })); await screen.findByText(t.credentials.verification.status.succeeded)
    expect(env.reads).toContain(id); expect(attempts).toBe(1)
    await user.click(screen.getByRole('button', { name: t.credentials.verification.again })); await waitFor(() => expect(attempts).toBe(2))
  })
  it('a 412 UUID version conflict does not switch to the new version or POST again', async () => {
    const user = userEvent.setup(), env = setup()
    server.use(http.post(apiUrl(base), () => HttpResponse.json({ error: { code: 'precondition_failed', message: 'The account version has changed', details: { projectId: account.projectId, accountId: account.id, expectedVersionId: account.currentVersionId, currentVersionId: '20000000-0000-7000-8000-000000000002' } } }, { status: 412 })))
    renderWithQuery(<CLIConnectionVerification account={account} />); await readReady(); await user.click(screen.getByRole('button', { name: t.credentials.verification.test }))
    await screen.findByText(t.credentials.verification.changed); expect(screen.getByRole('button', { name: t.credentials.verification.test })).toBeDisabled(); expect(env.posts).toHaveLength(0)
  })
  it('a 409 active conflict reads the original operation from details and does not cancel or resend', async () => {
    const user = userEvent.setup(), env = setup(); let posts = 0
    server.use(http.post(apiUrl(base), () => { posts++; env.set(verificationOperation()); return HttpResponse.json({ error: { code: 'verification_in_progress', message: 'A verification is already running', details: { projectId: account.projectId, accountId: account.id, operationId: verificationOperation().id, versionId: account.currentVersionId } } }, { status: 409 }) }))
    renderWithQuery(<CLIConnectionVerification account={account} />); await readReady(); await user.click(screen.getByRole('button', { name: t.credentials.verification.test }))
    await screen.findByText(t.credentials.verification.phase.preparing_tool); expect(env.reads).toContain(verificationOperation().id); expect(posts).toBe(1)
  })
  it.each([401, 403, 404, 503])('GET %d means neither untested nor unsupported by an old endpoint', async status => {
    setup(); server.use(http.get(apiUrl(base), () => HttpResponse.json(errorBody(status === 404 ? 'not_found' : status === 503 ? 'internal_error' : 'unauthenticated'), { status })))
    renderWithQuery(<CLIConnectionVerification account={account} />)
    await screen.findByRole('alert'); expect(screen.queryByText(t.credentials.verification.empty)).toBeNull(); expect(screen.queryByText(t.credentials.verification.unsupported)).toBeNull()
    expect(screen.getByRole('button', { name: t.credentials.verification.test })).toBeDisabled()
  })
  // An older server's fixed Chinese text “接口不存在” (No such endpoint) is covered in
  // src/zh-CN/features/project-credentials/CLIConnectionVerification.zh.test.tsx.
  it.each([
    ['the unknown_endpoint signal', unknownEndpointBody()],
  ])('only an old router\'s fixed missing-endpoint 404 means unsupported (%s)', async (_, body) => {
    setup(); server.use(http.get(apiUrl(base), () => HttpResponse.json(body, { status: 404 })))
    renderWithQuery(<CLIConnectionVerification account={account} />)
    await screen.findByText(t.credentials.verification.unsupported); expect(screen.queryByText(t.credentials.verification.empty)).toBeNull()
  })
  it('after the current version is replaced, a late old success is not shown as a new success; renaming does not test again', async () => {
    const env = setup(verificationPassed()), view = renderWithQuery(<CLIConnectionVerification account={account} />)
    await screen.findByText(t.credentials.verification.status.succeeded)
    env.set(null); const next = { ...account, currentVersionId: '20000000-0000-7000-8000-000000000002' }
    server.use(http.get(apiUrl(base), () => HttpResponse.json({ accountId: account.id, currentVersionId: next.currentVersionId, operation: null })))
    view.rerender(<CLIConnectionVerification account={next} />)
    await readReady(); expect(screen.queryByText(t.credentials.verification.status.succeeded)).toBeNull(); expect(env.posts).toHaveLength(0)
  })
  it('after cancelling, an old pending GET and old state do not overwrite the terminal state', async () => {
    const user = userEvent.setup(), pending = verificationOperation(), env = setup(pending)
    const cancelled = verificationOperation({ status: 'cancelled', phase: null, completedAt: '2026-10-16T10:01:00Z', result: { outcome: 'cancelled', verifiedAt: null, durationMs: 1, errorCode: null, httpStatus: null } })
    server.use(http.post(apiUrl(`${base}/${pending.id}/cancel`), () => HttpResponse.json(cancelled)))
    const view = renderWithQuery(<CLIConnectionVerification account={account} />)
    await screen.findByText(t.credentials.verification.phase.preparing_tool); await user.click(screen.getByRole('button', { name: t.credentials.verification.cancel }))
    await screen.findByText(t.credentials.verification.status.cancelled)
    await act(() => view.client.invalidateQueries({ queryKey: verificationKeys.account(account.projectId, account.id, account.currentVersionId!) }))
    expect(screen.getByText(t.credentials.verification.status.cancelled)).toBeInTheDocument(); expect(env.posts).toHaveLength(0)
  })
  it('with several accounts, a pending test locks only its own account and a failure does not affect another account', async () => {
    setup(verificationOperation()); const other = { ...account, id: '10000000-0000-7000-8000-000000000002', name: 'Another account' }
    server.use(http.get(apiUrl(`/projects/${other.projectId}/credential-accounts/${other.id}/verification-operations`), () => HttpResponse.json({ accountId: other.id, currentVersionId: other.currentVersionId, operation: null })))
    renderWithQuery(<><CLIConnectionVerification account={account} /><CLIConnectionVerification account={other} /></>)
    await screen.findByText(t.credentials.verification.empty)
    expect(within(screen.getByRole('region', { name: `${t.credentials.verification.title} · Another account` })).getByRole('button', { name: t.credentials.verification.test })).toBeEnabled()
    expect(within(screen.getByRole('region', { name: `${t.credentials.verification.title} · Database account` })).getByRole('button', { name: t.credentials.verification.again })).toBeDisabled()
  })
  it.each(['timeout', 'tls_failed', 'provider_policy'] as const)('%s safe failure is not retried automatically', async errorCode => {
    const env = setup(verificationOperation({ status: 'failed', phase: null, completedAt: '2026-10-16T10:01:00Z', result: { outcome: 'failed', verifiedAt: null, durationMs: 1, errorCode, httpStatus: null } }))
    renderWithQuery(<CLIConnectionVerification account={account} />)
    await screen.findByText(t.credentials.verification.errors[errorCode]); expect(env.posts).toHaveLength(0); expect(screen.getByRole('button', { name: t.credentials.verification.again })).toBeEnabled()
  })
  it('an account replacement draft is not tested; a failed test keeps the secret draft and the saved status', async () => {
    const user = userEvent.setup(), env = setup()
    server.use(
      http.get(apiUrl('/model-providers'), () => HttpResponse.json({ providers: [] })),
      http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [{ name: 'mysql', version: '8.4.9', fields: [{ name: 'password', label: 'Password', type: 'secret', required: true }], injection: 'files', install: { status: 'installed' }, warnings: [] }] })),
      http.get(apiUrl(`/projects/${account.projectId}/credential-accounts`), () => HttpResponse.json({ projectId: account.projectId, credentialsRevision: 1, accounts: [account] })),
      http.get(apiUrl(`/projects/${account.projectId}/credential-account-impact`), () => HttpResponse.json({ projectId: account.projectId, accountId: account.id, credentialsRevision: 1, agents: [] })),
      http.post(apiUrl(base), async ({ request }) => { const body = await request.json() as Schemas['CLIVerificationStart']; env.posts.push(body); const op = verificationOperation({ id: body.operationId, status: 'failed', phase: null, completedAt: '2026-10-16T10:01:00Z', result: { outcome: 'failed', verifiedAt: null, durationMs: 1, errorCode: 'tls_failed', httpStatus: null } }); env.set(op); return HttpResponse.json(op, { status: 202 }) }),
    )
    renderWithQuery(<DirtyContext.Provider value={() => {}}><ProjectAccounts projectId={account.projectId} purpose="cli" /></DirtyContext.Provider>)
    await user.click(await screen.findByRole('button', { name: 'Verify' }))
    await readReady(); await user.click(screen.getByRole('button', { name: 'Replace credential' }))
    await user.type(screen.getByLabelText(/^Password/), 'synthetic-unsaved-password')
    expect(env.posts).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: t.credentials.verification.test }))
    await screen.findByText(t.credentials.verification.errors.tls_failed)
    expect(screen.getByLabelText(/^Password/)).toHaveValue('synthetic-unsaved-password')
    expect(screen.getByText('Saved', { exact: true })).toBeInTheDocument()
    expect(env.posts[0]).toEqual({ operationId: expect.any(String), expectedVersionId: account.currentVersionId })
  })
  it('deleted or non-builtin accounts get no verification entry', () => {
    setup(); renderWithQuery(<><CLIConnectionVerification account={{ ...account, status: 'deleted' }} /><CLIConnectionVerification account={{ ...account, cli: 'custom-cli' }} /></>)
    expect(screen.queryByRole('region')).toBeNull()
  })

  it('the terminal state only invalidates the tool GET cache and does not write a passed test as installed', async () => {
    const user = userEvent.setup(), env = setup(); let installed = false, reads = 0
    server.use(http.get(apiUrl('/cli-templates'), () => { reads++; return HttpResponse.json({ templates: [{ name: 'mysql', version: '8.4.9', fields: [], injection: 'files', install: { status: installed ? 'installed' : 'not_installed' }, warnings: [] }] }) }),
      http.post(apiUrl(base), async ({ request }) => { const body = await request.json() as Schemas['CLIVerificationStart']; env.posts.push(body); installed = true; const op = verificationPassed({ id: body.operationId }); env.set(op); return HttpResponse.json(op, { status: 202 }) }))
    function View() { const templates = useCliTemplates(); return <>{templates.data && <CliToolStatus template={templates.data.templates[0]} />}<CLIConnectionVerification account={account} /></> }
    renderWithQuery(<View />); await readReady(); expect(screen.getByText('Tool setup: Not installed · mysql v8.4.9')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: t.credentials.verification.test })); await screen.findByText('Tool setup: Installed · mysql v8.4.9')
    expect(reads).toBeGreaterThan(1); expect(env.posts).toHaveLength(1)
  })

  it('verification_unsupported names its reason in the UI language', async () => {
    const user = userEvent.setup(); setup()
    server.use(http.post(apiUrl(base), () => HttpResponse.json(errorBody('verification_unsupported', 'Template does not support verification', { details: { cli: 'mysql', templateSource: 'builtin', reason: 'template_fingerprint_mismatch' } }), { status: 422 })))
    renderWithQuery(<CLIConnectionVerification account={account} />); await readReady(); await user.click(screen.getByRole('button', { name: t.credentials.verification.test }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Connection tests aren\'t supported for this tool or template yet: The account\'s template doesn\'t match the platform\'s built-in verification template')
    expect(screen.queryByText(/Template does not support verification/)).toBeNull()
  })
  it('the account template status is named, not shown as a code', async () => {
    setup()
    const withTemplate = { ...account, template: { source: 'builtin', credentialFormat: '1', fieldNames: ['password'], warnings: [], status: 'incompatible' } }
    server.use(
      http.get(apiUrl('/model-providers'), () => HttpResponse.json({ providers: [] })),
      http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [{ name: 'mysql', version: '8.4.9', fields: [{ name: 'password', label: 'Password', type: 'secret', required: true }], injection: 'files', install: { status: 'installed' }, warnings: [] }] })),
      http.get(apiUrl(`/projects/${account.projectId}/credential-accounts`), () => HttpResponse.json({ projectId: account.projectId, credentialsRevision: 1, accounts: [withTemplate] })),
    )
    renderWithQuery(<DirtyContext.Provider value={() => {}}><ProjectAccounts projectId={account.projectId} purpose="cli" /></DirtyContext.Provider>)
    expect(await screen.findByText(/template status incompatible with the current template/)).toBeInTheDocument()
    expect(screen.queryByText(/template status incompatible ·/)).toBeNull()
  })
})
