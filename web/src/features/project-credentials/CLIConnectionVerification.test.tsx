import { act, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { verificationKeys } from '@/api/cli-verification'
import { apiUrl, errorBody } from '@/test/api'
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

describe('五工具手动验证正式HTTP fixture', () => {
  it.each(['mysql', 'psql', 'gh', 'gcloud', 'lark-cli'])('%s读取和改名不POST，仅测试已保存版本', async cli => {
    const env = setup(), view = renderWithQuery(<CLIConnectionVerification account={{ ...account, cli }} />)
    await readReady(); expect(env.posts).toHaveLength(0)
    view.rerender(<CLIConnectionVerification account={{ ...account, cli, name: '新备注', revision: 2 }} />)
    expect(screen.getByRole('button', { name: t.credentials.verification.test })).toBeEnabled()
    expect(env.posts).toHaveLength(0); expect(screen.getByText(t.credentials.verification.scope)).toBeInTheDocument()
  })
  it('明确点击才POST一次，固定版本且无草稿/命令/秘密', async () => {
    const user = userEvent.setup(), env = setup(); renderWithQuery(<CLIConnectionVerification account={account} />)
    await readReady(); await user.click(screen.getByRole('button', { name: t.credentials.verification.test }))
    await screen.findByText(t.credentials.verification.phase.preparing_tool)
    expect(env.posts).toHaveLength(1); expect(env.posts[0]).toEqual({ operationId: expect.any(String), expectedVersionId: account.currentVersionId })
    expect(screen.getByRole('button', { name: t.credentials.verification.again })).toBeDisabled()
  })
  it.each([
    ['mysql', 'database_select1'], ['psql', 'database_select1'], ['gh', 'github_user'], ['gcloud', 'google_crm_read'], ['lark-cli', 'lark_bot'],
  ] as const)('%s通过只报告固定深度及验证时间', async (cli, probeKind) => {
    setup(verificationPassed({ cli, probeKind })); renderWithQuery(<CLIConnectionVerification account={{ ...account, cli }} />)
    await screen.findByText(t.credentials.verification.status.succeeded)
    expect(screen.getByText(t.credentials.verification.depth[probeKind])).toBeInTheDocument()
    expect(screen.getByText(`${t.credentials.verification.time}2026-10-16T10:00:10Z`)).toBeInTheDocument()
  })
  it('未知POST只GET原ID，读取到终态后显式重试才新attempt', async () => {
    const user = userEvent.setup(), env = setup(); let id = '', attempts = 0
    server.use(http.post(apiUrl(base), async ({ request }) => { attempts++; id = (await request.json() as Schemas['CLIVerificationStart']).operationId; env.set(verificationPassed({ id })); return HttpResponse.json(errorBody('internal_error'), { status: 502 }) }))
    renderWithQuery(<CLIConnectionVerification account={account} />); await readReady()
    await user.click(screen.getByRole('button', { name: t.credentials.verification.test })); await screen.findByText(t.credentials.verification.status.succeeded)
    expect(env.reads).toContain(id); expect(attempts).toBe(1)
    await user.click(screen.getByRole('button', { name: t.credentials.verification.again })); await waitFor(() => expect(attempts).toBe(2))
  })
  it('412 UUID版本冲突不改投新版本、不重POST', async () => {
    const user = userEvent.setup(), env = setup()
    server.use(http.post(apiUrl(base), () => HttpResponse.json({ error: { code: 'precondition_failed', message: '版本已变', details: { projectId: account.projectId, accountId: account.id, expectedVersionId: account.currentVersionId, currentVersionId: '20000000-0000-7000-8000-000000000002' } } }, { status: 412 })))
    renderWithQuery(<CLIConnectionVerification account={account} />); await readReady(); await user.click(screen.getByRole('button', { name: t.credentials.verification.test }))
    await screen.findByText(t.credentials.verification.changed); expect(screen.getByRole('button', { name: t.credentials.verification.test })).toBeDisabled(); expect(env.posts).toHaveLength(0)
  })
  it('409活动冲突按details读取原operation，不取消或重发', async () => {
    const user = userEvent.setup(), env = setup(); let posts = 0
    server.use(http.post(apiUrl(base), () => { posts++; env.set(verificationOperation()); return HttpResponse.json({ error: { code: 'verification_in_progress', message: '已有操作', details: { projectId: account.projectId, accountId: account.id, operationId: verificationOperation().id, versionId: account.currentVersionId } } }, { status: 409 }) }))
    renderWithQuery(<CLIConnectionVerification account={account} />); await readReady(); await user.click(screen.getByRole('button', { name: t.credentials.verification.test }))
    await screen.findByText(t.credentials.verification.phase.preparing_tool); expect(env.reads).toContain(verificationOperation().id); expect(posts).toBe(1)
  })
  it.each([401, 403, 404, 503])('GET%d不是未测试或旧接口不支持', async status => {
    setup(); server.use(http.get(apiUrl(base), () => HttpResponse.json(errorBody(status === 404 ? 'not_found' : status === 503 ? 'internal_error' : 'unauthenticated'), { status })))
    renderWithQuery(<CLIConnectionVerification account={account} />)
    await screen.findByRole('alert'); expect(screen.queryByText(t.credentials.verification.empty)).toBeNull(); expect(screen.queryByText(t.credentials.verification.unsupported)).toBeNull()
    expect(screen.getByRole('button', { name: t.credentials.verification.test })).toBeDisabled()
  })
  it('仅旧路由固定接口不存在404提示不支持', async () => {
    setup(); server.use(http.get(apiUrl(base), () => HttpResponse.json(errorBody('not_found', '接口不存在'), { status: 404 })))
    renderWithQuery(<CLIConnectionVerification account={account} />)
    await screen.findByText(t.credentials.verification.unsupported); expect(screen.queryByText(t.credentials.verification.empty)).toBeNull()
  })
  it('替换当前版本后迟到旧成功不冒新成功，改名不会重新测试', async () => {
    const env = setup(verificationPassed()), view = renderWithQuery(<CLIConnectionVerification account={account} />)
    await screen.findByText(t.credentials.verification.status.succeeded)
    env.set(null); const next = { ...account, currentVersionId: '20000000-0000-7000-8000-000000000002' }
    server.use(http.get(apiUrl(base), () => HttpResponse.json({ accountId: account.id, currentVersionId: next.currentVersionId, operation: null })))
    view.rerender(<CLIConnectionVerification account={next} />)
    await readReady(); expect(screen.queryByText(t.credentials.verification.status.succeeded)).toBeNull(); expect(env.posts).toHaveLength(0)
  })
  it('取消后旧pending GET与旧state不覆盖终态', async () => {
    const user = userEvent.setup(), pending = verificationOperation(), env = setup(pending)
    const cancelled = verificationOperation({ status: 'cancelled', phase: null, completedAt: '2026-10-16T10:01:00Z', result: { outcome: 'cancelled', verifiedAt: null, durationMs: 1, errorCode: null, httpStatus: null } })
    server.use(http.post(apiUrl(`${base}/${pending.id}/cancel`), () => HttpResponse.json(cancelled)))
    const view = renderWithQuery(<CLIConnectionVerification account={account} />)
    await screen.findByText(t.credentials.verification.phase.preparing_tool); await user.click(screen.getByRole('button', { name: t.credentials.verification.cancel }))
    await screen.findByText(t.credentials.verification.status.cancelled)
    await act(() => view.client.invalidateQueries({ queryKey: verificationKeys.account(account.projectId, account.id, account.currentVersionId!) }))
    expect(screen.getByText(t.credentials.verification.status.cancelled)).toBeInTheDocument(); expect(env.posts).toHaveLength(0)
  })
  it('多个账号的pending只锁本人，失败不影响另一账号', async () => {
    setup(verificationOperation()); const other = { ...account, id: '10000000-0000-7000-8000-000000000002', name: '另一账号' }
    server.use(http.get(apiUrl(`/projects/${other.projectId}/credential-accounts/${other.id}/verification-operations`), () => HttpResponse.json({ accountId: other.id, currentVersionId: other.currentVersionId, operation: null })))
    renderWithQuery(<><CLIConnectionVerification account={account} /><CLIConnectionVerification account={other} /></>)
    await screen.findByText(t.credentials.verification.empty)
    expect(within(screen.getByRole('region', { name: `${t.credentials.verification.title} · 另一账号` })).getByRole('button', { name: t.credentials.verification.test })).toBeEnabled()
    expect(within(screen.getByRole('region', { name: `${t.credentials.verification.title} · 数据库账号` })).getByRole('button', { name: t.credentials.verification.again })).toBeDisabled()
  })
  it.each(['timeout', 'tls_failed', 'provider_policy'] as const)('%s安全失败不自动重试', async errorCode => {
    const env = setup(verificationOperation({ status: 'failed', phase: null, completedAt: '2026-10-16T10:01:00Z', result: { outcome: 'failed', verifiedAt: null, durationMs: 1, errorCode, httpStatus: null } }))
    renderWithQuery(<CLIConnectionVerification account={account} />)
    await screen.findByText(t.credentials.verification.errors[errorCode]); expect(env.posts).toHaveLength(0); expect(screen.getByRole('button', { name: t.credentials.verification.again })).toBeEnabled()
  })
  it('账号替换草稿不参与测试，测试失败保留秘密草稿和保存状态', async () => {
    const user = userEvent.setup(), env = setup()
    server.use(
      http.get(apiUrl('/model-providers'), () => HttpResponse.json({ providers: [] })),
      http.get(apiUrl('/cli-templates'), () => HttpResponse.json({ templates: [{ name: 'mysql', version: '8.4.9', fields: [{ name: 'password', label: '密码', type: 'secret', required: true }], injection: 'files', install: { status: 'installed' }, warnings: [] }] })),
      http.get(apiUrl(`/projects/${account.projectId}/credential-accounts`), () => HttpResponse.json({ projectId: account.projectId, credentialsRevision: 1, accounts: [account] })),
      http.get(apiUrl(`/projects/${account.projectId}/credential-account-impact`), () => HttpResponse.json({ projectId: account.projectId, accountId: account.id, credentialsRevision: 1, agents: [] })),
      http.post(apiUrl(base), async ({ request }) => { const body = await request.json() as Schemas['CLIVerificationStart']; env.posts.push(body); const op = verificationOperation({ id: body.operationId, status: 'failed', phase: null, completedAt: '2026-10-16T10:01:00Z', result: { outcome: 'failed', verifiedAt: null, durationMs: 1, errorCode: 'tls_failed', httpStatus: null } }); env.set(op); return HttpResponse.json(op, { status: 202 }) }),
    )
    renderWithQuery(<DirtyContext.Provider value={() => {}}><ProjectAccounts projectId={account.projectId} purpose="cli" /></DirtyContext.Provider>)
    await readReady(); await user.click(screen.getByRole('button', { name: '整体替换凭证' }))
    await user.type(screen.getByLabelText(/^密码/), 'synthetic-unsaved-password')
    expect(env.posts).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: t.credentials.verification.test }))
    await screen.findByText(t.credentials.verification.errors.tls_failed)
    expect(screen.getByLabelText(/^密码/)).toHaveValue('synthetic-unsaved-password')
    expect(screen.getByText('已保存', { exact: true })).toBeInTheDocument()
    expect(env.posts[0]).toEqual({ operationId: expect.any(String), expectedVersionId: account.currentVersionId })
  })
  it('删除或非builtin账号不增加验证入口', () => {
    setup(); renderWithQuery(<><CLIConnectionVerification account={{ ...account, status: 'deleted' }} /><CLIConnectionVerification account={{ ...account, cli: 'custom-cli' }} /></>)
    expect(screen.queryByRole('region')).toBeNull()
  })

  it('终态只失效工具GET缓存，不把测试通过直接写成已安装', async () => {
    const user = userEvent.setup(), env = setup(); let installed = false, reads = 0
    server.use(http.get(apiUrl('/cli-templates'), () => { reads++; return HttpResponse.json({ templates: [{ name: 'mysql', version: '8.4.9', fields: [], injection: 'files', install: { status: installed ? 'installed' : 'not_installed' }, warnings: [] }] }) }),
      http.post(apiUrl(base), async ({ request }) => { const body = await request.json() as Schemas['CLIVerificationStart']; env.posts.push(body); installed = true; const op = verificationPassed({ id: body.operationId }); env.set(op); return HttpResponse.json(op, { status: 202 }) }))
    function View() { const templates = useCliTemplates(); return <>{templates.data && <CliToolStatus template={templates.data.templates[0]} />}<CLIConnectionVerification account={account} /></> }
    renderWithQuery(<View />); await readReady(); expect(screen.getByText('工具准备：未安装 · mysql v8.4.9')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: t.credentials.verification.test })); await screen.findByText('工具准备：已安装 · mysql v8.4.9')
    expect(reads).toBeGreaterThan(1); expect(env.posts).toHaveLength(1)
  })

})
