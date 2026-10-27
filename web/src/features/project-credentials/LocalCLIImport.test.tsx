import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { useParams } from 'react-router'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { importKey } from '@/api/cli-import'
import { apiUrl, defaultProject, errorBody } from '@/test/api'
import { importCandidate, importOperation, importScan } from '@/test/cli-import'
import { importPublicCertificate } from '@/test/cli-import-certificate'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { DraftNavigationGuard } from '@/features/prompts/DraftNavigationGuard'
import { LocalCLIImport } from './LocalCLIImport'
import { importText as copy } from '@/i18n/cli-import'
import { publicCertificatePEM, supplementProblem } from './import-selection'

const base = `/projects/${defaultProject.id}/cli-credential-import`
function setup(scan = importScan()) {
  let op = importOperation(), scans = 0
  const starts: Schemas['CLIImportStart'][] = [], reads: string[] = []
  server.use(http.post(apiUrl(`${base}/scans`), async ({ request }) => { expect(await request.json()).toEqual({}); scans++; return HttpResponse.json(scan, { status: 201 }) }),
    http.post(apiUrl(`${base}/imports`), async ({ request }) => { const body = await request.json() as Schemas['CLIImportStart']; starts.push(body); op = importOperation({ id: body.operationId, scanId: body.scanId, status: 'succeeded', phase: null, credentialsRevision: 6, completedAt: '2026-10-16T10:00:10Z', items: body.candidateIds.map(id => ({ candidateId: id, cli: 'gh', label: '合成账号', status: 'imported', accountId: '54000000-0000-7000-8000-000000000001', versionId: '55000000-0000-7000-8000-000000000001', errorCode: null })) }); return HttpResponse.json({ operation: op }, { status: 202 }) }),
    http.get(apiUrl(`${base}/imports/:id`), ({ params }) => { reads.push(String(params.id)); return HttpResponse.json({ operation: op }) }))
  return { starts, reads, scanCalls: () => scans, set: (next: typeof op) => { op = next } }
}
function render(projectId = defaultProject.id, initial = '/') {
  return renderRoutes([{ path: '/', element: <DraftNavigationGuard><LocalCLIImport projectId={projectId} projectName="当前合成项目" /></DraftNavigationGuard> }], initial)
}
async function discover(user: ReturnType<typeof userEvent.setup>) { await user.click(screen.getByRole('button', { name: copy.entry })); await user.click(screen.getByRole('button', { name: copy.scan })); await screen.findByRole('checkbox') }
async function confirm(user: ReturnType<typeof userEvent.setup>) { await user.click(screen.getByRole('checkbox')); await user.click(screen.getByRole('button', { name: copy.next })); await user.click(screen.getByRole('button', { name: copy.confirm })) }

describe('本机导入正式合同 Web fixture', () => {
  it('读页/打开入口不扫描，主动扫描后全未选，确认后才保存', async () => {
    const user = userEvent.setup(), env = setup(); render()
    expect(env.scanCalls()).toBe(0); await user.click(screen.getByRole('button', { name: copy.entry })); expect(env.scanCalls()).toBe(0)
    await user.click(screen.getByRole('button', { name: copy.scan })); expect(await screen.findByRole('checkbox')).not.toBeChecked(); expect(env.starts).toHaveLength(0)
    await user.click(screen.getByRole('checkbox')); await user.click(screen.getByRole('button', { name: copy.next })); expect(screen.getByText(`${copy.project}当前合成项目`)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: copy.confirm })); await screen.findByText(copy.status.succeeded)
    expect(env.starts).toEqual([{ operationId: expect.any(String), scanId: importScan().scanId, candidateIds: [importCandidate().candidateId], expectedCredentialsRevision: 5, supplements: [] }]); expect(screen.getByText(copy.savedOnly)).toBeInTheDocument()
  })
  it('CA依Supplementable白名单不依Missing子集，补齐公开字段才勾选', async () => {
    const user = userEvent.setup(), candidate = importCandidate({ cli: 'mysql', status: 'needs_fields', missingFields: ['database', 'sslMode'], supplementableFields: ['database', 'sslMode', 'caCert'] }), env = setup(importScan({ candidates: [candidate] })); render(); await discover(user)
    expect(screen.getByRole('checkbox')).toBeDisabled(); await user.type(screen.getByLabelText(copy.field.database), 'fixture_db'); await user.selectOptions(screen.getByLabelText(copy.field.sslMode), 'VERIFY_IDENTITY')
    expect(screen.getByRole('checkbox')).toBeDisabled(); await user.click(screen.getByLabelText(copy.field.caCert)); await user.paste(importPublicCertificate)
    expect(screen.getByRole('checkbox')).toBeEnabled(); await confirm(user); await screen.findByText(copy.status.succeeded)
    expect(env.starts[0].supplements).toEqual([{ candidateId: candidate.candidateId, database: 'fixture_db', sslMode: 'VERIFY_IDENTITY', caCert: importPublicCertificate }])
  })
  it('已有字段无补填入口，缺秘密不能伪成本机可导入', async () => {
    const user = userEvent.setup(); setup(importScan({ candidates: [importCandidate({ status: 'needs_fields', missingFields: ['password'], supplementableFields: [] })] })); render(); await discover(user)
    expect(screen.getByRole('checkbox')).toBeDisabled(); expect(screen.queryByRole('textbox')).toBeNull(); expect(screen.getByText(copy.missingSecret)).toBeInTheDocument()
  })
  it('公开CA拒绝私钥和垃圾；disable+CA非法，不猜TLS默认', () => {
    expect(publicCertificatePEM(importPublicCertificate)).toBe(true); expect(publicCertificatePEM(importPublicCertificate+'garbage')).toBe(false)
    expect(publicCertificatePEM('-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----')).toBe(false)
    const c = importCandidate({ cli: 'mysql', supplementableFields: ['sslMode', 'caCert'] })
    expect(supplementProblem(c, { sslMode: 'DISABLED', caCert: importPublicCertificate })).toBe(copy.invalidTLS)
    expect(supplementProblem(c, { sslMode: 'PREFERRED' })).toBe(copy.invalidTLS)
    expect(supplementProblem(importCandidate(), { database: 'override' })).toBe(copy.errors.supplement_override)
  })
  it.each(['unsupported', 'unreadable', 'requires_platform_login'] as const)('%s候选明确显示且不可选择', async status => {
    const user = userEvent.setup(); setup(importScan({ candidates: [importCandidate({ status })] })); render(); await discover(user)
    expect(screen.getByRole('checkbox')).toBeDisabled(); expect(screen.getByText(copy.candidate[status])).toBeInTheDocument()
  })
  it('历史来源只提示风险，不按同名合并或自动勾选', async () => {
    const user = userEvent.setup(); setup(importScan({ candidates: [importCandidate({ previouslyImported: true }), importCandidate({ candidateId: '51000000-0000-7000-8000-000000000002', previouslyImported: true })] })); render(); await user.click(screen.getByRole('button', { name: copy.entry })); await user.click(screen.getByRole('button', { name: copy.scan }))
    await screen.findAllByRole('checkbox'); screen.getAllByRole('checkbox').forEach(c => expect(c).not.toBeChecked()); expect(screen.getAllByText(copy.previous)).toHaveLength(2)
  })
  it('未知提交保原ID仅GET恢复，不自动新批次/重扫', async () => {
    const user = userEvent.setup(), env = setup(); let id = '', count = 0
    server.use(http.post(apiUrl(`${base}/imports`), async ({ request }) => { count++; id = (await request.json() as Schemas['CLIImportStart']).operationId; env.set(importOperation({ id, status: 'succeeded', phase: null, credentialsRevision: 6, completedAt: '2026-10-16T10:00:10Z' })); return HttpResponse.json(errorBody('internal_error'), { status: 502 }) }))
    const view = render(); await discover(user); await confirm(user); await screen.findByText(copy.status.succeeded)
    expect(count).toBe(1); expect(env.scanCalls()).toBe(1); expect(env.reads).toContain(id); expect(view.router.state.location.search).toContain(id)
  })
  it('刷新操作深链只GET原操作，不扫描或导入', async () => {
    const env = setup(); render(defaultProject.id, `/?cliImport=${importOperation().id}`)
    await screen.findByText(copy.status.running); expect(env.reads).toContain(importOperation().id); expect(env.scanCalls()).toBe(0); expect(env.starts).toHaveLength(0)
  })
  it('整批失败显示具体失败及未提交，不伪报部分已保存', async () => {
    const user = userEvent.setup(), env = setup()
    server.use(http.post(apiUrl(`${base}/imports`), async ({ request }) => { const id = (await request.json() as Schemas['CLIImportStart']).operationId; const op = importOperation({ id, status: 'failed', phase: null, completedAt: '2026-10-16T10:00:10Z', errorCode: 'source_changed', items: [{ ...importOperation().items[0], status: 'failed', errorCode: 'source_changed' }, { ...importOperation().items[0], candidateId: '51000000-0000-7000-8000-000000000002', status: 'not_committed' }] }); env.set(op); return HttpResponse.json({ operation: op }, { status: 202 }) }))
    render(); await discover(user); await confirm(user); await screen.findByText(copy.status.failed); expect(screen.getByText(copy.item.not_committed)).toBeInTheDocument(); expect(screen.queryByText(copy.item.imported)).toBeNull()
  })
  it('取消后旧running GET不覆终态', async () => {
    const user = userEvent.setup(), env = setup(), op = importOperation(); env.set(op)
    server.use(http.post(apiUrl(`${base}/imports/${op.id}/cancel`), async ({ request }) => { expect(await request.json()).toEqual({}); return HttpResponse.json({ operation: { ...op, status: 'cancelled', phase: null, completedAt: '2026-10-16T10:00:10Z' } }) }))
    const view = render(defaultProject.id, `/?cliImport=${op.id}`); await screen.findByText(copy.status.running); await user.click(screen.getByRole('button', { name: copy.cancel })); await screen.findByText(copy.status.cancelled)
    await act(() => view.client.invalidateQueries({ queryKey: importKey(defaultProject.id, op.id) })); expect(screen.getByText(copy.status.cancelled)).toBeInTheDocument()
  })
  it.each(['import_scan_expired','import_source_changed','precondition_failed'] as const)('%s拒绝不自动重扫/重发且保选项', async code => {
    const user = userEvent.setup(), env = setup(); let calls = 0
    server.use(http.post(apiUrl(`${base}/imports`), () => { calls++; return HttpResponse.json(errorBody(code), { status: code === 'import_scan_expired' ? 409 : 412 }) }))
    render(); await discover(user); await confirm(user); await screen.findByRole('alert'); expect(calls).toBe(1); expect(env.scanCalls()).toBe(1); await user.click(screen.getByRole('button', { name: copy.back })); expect(screen.getByRole('checkbox')).toBeChecked()
  })
  it('过期scan不能发Start，未知GET失败不当未提交', async () => {
    const user = userEvent.setup(), env = setup(importScan({ expiresAt: '2020-01-01T00:00:00Z' })); render(); await discover(user)
    expect(screen.getByRole('checkbox')).toBeDisabled(); expect(env.starts).toHaveLength(0); expect(screen.getByText(copy.expired)).toBeInTheDocument()
  })
  it.each([403,404,503])('GET%d不伪报成功/失败终态、不重扫描', async status => {
    const env = setup(); server.use(http.get(apiUrl(`${base}/imports/:id`), () => HttpResponse.json(errorBody(status === 404 ? 'not_found' : status === 503 ? 'internal_error' : 'forbidden'), { status })))
    render(defaultProject.id, `/?cliImport=${importOperation().id}`); await screen.findByRole('alert'); expect(screen.getByText(copy.unknown)).toBeInTheDocument(); expect(screen.queryByRole('button', { name: copy.clearInvalidId })).toBeNull(); expect(env.starts).toHaveLength(0); expect(env.scanCalls()).toBe(0)
  })
  it('提交先赢的cancel返回succeeded，不能假报回滚', async () => {
    const user = userEvent.setup(), env = setup(), op = importOperation(); env.set(op)
    server.use(http.post(apiUrl(`${base}/imports/${op.id}/cancel`), () => HttpResponse.json({ operation: { ...op, status: 'succeeded', phase: null, completedAt: '2026-10-16T10:00:10Z', credentialsRevision: 6, items: [{ ...op.items[0], status: 'imported', accountId: '54000000-0000-7000-8000-000000000001', versionId: '55000000-0000-7000-8000-000000000001' }] } })))
    render(defaultProject.id, `/?cliImport=${op.id}`); await screen.findByText(copy.status.running); await user.click(screen.getByRole('button', { name: copy.cancel })); await screen.findByText(copy.status.succeeded)
    expect(screen.queryByText(copy.status.cancelled)).toBeNull(); expect(screen.getByText(copy.item.imported)).toBeInTheDocument()
  })
  it('A扫描迟到不能清B操作定位或把A候选带到B', async () => {
    const user = userEvent.setup(), env = setup(), other = '56000000-0000-7000-8000-000000000001', otherOp = '57000000-0000-7000-8000-000000000001'
    let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve })
    server.use(http.post(apiUrl(`${base}/scans`), async () => { await gate; return HttpResponse.json(importScan(), { status: 201 }) }),
      http.get(apiUrl(`/projects/${other}/cli-credential-import/imports/${otherOp}`), () => HttpResponse.json({ operation: importOperation({ id: otherOp, projectId: other, status: 'failed', phase: null, completedAt: '2026-10-16T10:00:10Z', errorCode: 'worker_restarted' }) })))
    function Page() { const { name } = useParams(); return <DraftNavigationGuard><LocalCLIImport projectId={name === 'A' ? defaultProject.id : other} projectName={name} /></DraftNavigationGuard> }
    const view = renderRoutes([{ path: '/p/:name', element: <Page /> }], '/p/A')
    await user.click(screen.getByRole('button', { name: copy.entry })); await user.click(screen.getByRole('button', { name: copy.scan }))
    await act(() => view.router.navigate(`/p/B?cliImport=${otherOp}`)); await screen.findByText(copy.errors.worker_restarted)
    await act(async () => { release(); await gate }); await waitFor(() => expect(view.router.state.location.search).toContain(otherOp))
    expect(screen.queryByRole('checkbox')).toBeNull(); expect(env.starts).toHaveLength(0)
  })
  it('最多选择20项，不提供自动全量导入', async () => {
    const user = userEvent.setup(), env = setup(importScan({ candidates: Array.from({ length: 21 }, (_, i) => importCandidate({ candidateId: `51000000-0000-7000-8000-${String(i+1).padStart(12, '0')}`, label: `候选${i+1}` })) })); render()
    await user.click(screen.getByRole('button', { name: copy.entry })); await user.click(screen.getByRole('button', { name: copy.scan })); const boxes = await screen.findAllByRole('checkbox')
    for (const box of boxes) await user.click(box)
    expect(screen.getByText(copy.max)).toBeInTheDocument(); expect(boxes.filter(box => (box as HTMLInputElement).checked)).toHaveLength(20); expect(env.starts).toHaveLength(0)
  })

  it.each(['malformed', '00000000-0000-0000-0000-000000000000'])('无效定位%s可明确清除后手动扫描，绝不自动请求', async locator => {
    const user = userEvent.setup(), env = setup(), view = render(defaultProject.id, `/?cliImport=${locator}`)
    expect(screen.getByText(copy.invalidId)).toBeInTheDocument(); expect(env.scanCalls()).toBe(0); expect(env.starts).toHaveLength(0); expect(env.reads).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: copy.clearInvalidId }))
    expect(view.router.state.location.search).toBe(''); expect(screen.getByRole('button', { name: copy.scan })).toBeEnabled(); expect(env.scanCalls()).toBe(0); expect(env.starts).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: copy.scan })); expect(await screen.findByRole('checkbox')).not.toBeChecked(); expect(env.scanCalls()).toBe(1); expect(env.starts).toHaveLength(0)
  })

  it('重扫未完成时旧确认与补项均锁定，不发旧Start', async () => {
    const user = userEvent.setup(), candidate = importCandidate({ cli: 'mysql', status: 'needs_fields', missingFields: ['database'], supplementableFields: ['database'] }), env = setup(importScan({ candidates: [candidate] })); render(); await discover(user)
    await user.type(screen.getByLabelText(copy.field.database), 'original_db'); await user.click(screen.getByRole('checkbox')); await user.click(screen.getByRole('button', { name: copy.next }))
    let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve })
    server.use(http.post(apiUrl(`${base}/scans`), async () => { await gate; return HttpResponse.json(importScan({ candidates: [candidate], scanId: '52000000-0000-7000-8000-000000000002' }), { status: 201 }) }))
    await user.click(screen.getByRole('button', { name: copy.rescan })); expect(screen.getByRole('button', { name: copy.confirm })).toBeDisabled(); await user.click(screen.getByRole('button', { name: copy.confirm })); expect(env.starts).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: copy.back })); expect(screen.getByLabelText(copy.field.database)).toBeDisabled(); expect(screen.getByRole('checkbox')).toBeDisabled()
    await act(async () => { release(); await gate }); await waitFor(() => expect(screen.getByLabelText(copy.field.database)).toBeEnabled()); expect(screen.getByRole('checkbox')).not.toBeChecked(); expect(env.starts).toHaveLength(0)
  })
  it('同项目切到合法操作时迟到扫描不得清除新locator', async () => {
    const user = userEvent.setup(), env = setup(), op = importOperation(); env.set(op)
    let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve })
    server.use(http.post(apiUrl(`${base}/scans`), async () => { await gate; return HttpResponse.json(importScan(), { status: 201 }) }))
    const view = render(); await user.click(screen.getByRole('button', { name: copy.entry })); await user.click(screen.getByRole('button', { name: copy.scan }))
    await act(() => view.router.navigate(`/?cliImport=${op.id}`)); await screen.findByText(copy.status.running)
    await act(async () => { release(); await gate }); await waitFor(() => expect(screen.getByRole('button', { name: copy.cancel })).toBeEnabled())
    expect(view.router.state.location.search).toContain(op.id); expect(screen.queryByRole('checkbox')).toBeNull(); expect(env.starts).toHaveLength(0)
  })

  it('Start迟到4xx不能清同项目新操作定位或重造批次', async () => {
    const user = userEvent.setup(), env = setup(), newOp = importOperation(); env.set(newOp)
    let release!: () => void, posts = 0; const gate = new Promise<void>(resolve => { release = resolve })
    server.use(http.post(apiUrl(`${base}/imports`), async () => { posts++; await gate; return HttpResponse.json(errorBody('import_operation_conflict'), { status: 409 }) }))
    const view = render(); await discover(user); await confirm(user); await waitFor(() => expect(posts).toBe(1))
    await act(() => view.router.navigate(`/?cliImport=${newOp.id}`)); expect(view.router.state.location.search).toContain(newOp.id)
    await act(async () => { release(); await gate }); await screen.findByText(copy.status.running)
    expect(view.router.state.location.search).toContain(newOp.id); expect(posts).toBe(1); expect(env.scanCalls()).toBe(1); expect(screen.queryByRole('checkbox')).toBeNull()
  })

  it('合法大写UUID定位按同一canonical ID恢复200，零scan/start', async () => {
    const env = setup(), op = importOperation({ id: '53abcdef-abcd-7abc-8def-abcdef123456' }); env.set(op); render(defaultProject.id, `/?cliImport=${op.id.toUpperCase()}`)
    await screen.findByText(copy.status.running); expect(env.reads).toContain(op.id); expect(env.scanCalls()).toBe(0); expect(env.starts).toHaveLength(0); expect(screen.queryByText(copy.unknown)).toBeNull()
  })

})
