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
import { t } from '@/i18n'
import { publicCertificatePEM, supplementProblem } from './import-selection'

const copy = t.credentials.cliImport

const base = `/projects/${defaultProject.id}/cli-credential-import`
function setup(scan = importScan()) {
  let op = importOperation(), scans = 0
  const starts: Schemas['CLIImportStart'][] = [], reads: string[] = []
  server.use(http.post(apiUrl(`${base}/scans`), async ({ request }) => { expect(await request.json()).toEqual({}); scans++; return HttpResponse.json(scan, { status: 201 }) }),
    http.post(apiUrl(`${base}/imports`), async ({ request }) => { const body = await request.json() as Schemas['CLIImportStart']; starts.push(body); op = importOperation({ id: body.operationId, scanId: body.scanId, status: 'succeeded', phase: null, credentialsRevision: 6, completedAt: '2026-10-16T10:00:10Z', items: body.candidateIds.map(id => ({ candidateId: id, cli: 'gh', label: 'Synthetic account', status: 'imported', accountId: '54000000-0000-7000-8000-000000000001', versionId: '55000000-0000-7000-8000-000000000001', errorCode: null })) }); return HttpResponse.json({ operation: op }, { status: 202 }) }),
    http.get(apiUrl(`${base}/imports/:id`), ({ params }) => { reads.push(String(params.id)); return HttpResponse.json({ operation: op }) }))
  return { starts, reads, scanCalls: () => scans, set: (next: typeof op) => { op = next } }
}
function render(projectId = defaultProject.id, initial = '/') {
  return renderRoutes([{ path: '/', element: <DraftNavigationGuard><LocalCLIImport projectId={projectId} projectName="Current synthetic project" /></DraftNavigationGuard> }], initial)
}
async function discover(user: ReturnType<typeof userEvent.setup>) { await user.click(screen.getByRole('button', { name: copy.entry })); await user.click(screen.getByRole('button', { name: copy.scan })); await screen.findByRole('checkbox') }
async function confirm(user: ReturnType<typeof userEvent.setup>) { await user.click(screen.getByRole('checkbox')); await user.click(screen.getByRole('button', { name: copy.next })); await user.click(screen.getByRole('button', { name: copy.confirm })) }

describe('Local import, formal contract Web fixture', () => {
  it('reading the page or opening the entry does not scan; after an explicit scan nothing is selected, and saving happens only after confirming', async () => {
    const user = userEvent.setup(), env = setup(); render()
    expect(env.scanCalls()).toBe(0); await user.click(screen.getByRole('button', { name: copy.entry })); expect(env.scanCalls()).toBe(0)
    await user.click(screen.getByRole('button', { name: copy.scan })); expect(await screen.findByRole('checkbox')).not.toBeChecked(); expect(env.starts).toHaveLength(0)
    await user.click(screen.getByRole('checkbox')); await user.click(screen.getByRole('button', { name: copy.next })); expect(screen.getByText(`${copy.project}Current synthetic project`)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: copy.confirm })); await screen.findByText(copy.status.succeeded)
    expect(env.starts).toEqual([{ operationId: expect.any(String), scanId: importScan().scanId, candidateIds: [importCandidate().candidateId], expectedCredentialsRevision: 5, supplements: [] }]); expect(screen.getByText(copy.savedOnly)).toBeInTheDocument()
  })
  it('CA follows the supplementable allow list, not the missing subset; the box can be checked only after the public fields are filled in', async () => {
    const user = userEvent.setup(), candidate = importCandidate({ cli: 'mysql', status: 'needs_fields', missingFields: ['database', 'sslMode'], supplementableFields: ['database', 'sslMode', 'caCert'] }), env = setup(importScan({ candidates: [candidate] })); render(); await discover(user)
    expect(screen.getByRole('checkbox')).toBeDisabled(); await user.type(screen.getByLabelText(copy.field.database), 'fixture_db'); await user.selectOptions(screen.getByLabelText(copy.field.sslMode), 'VERIFY_IDENTITY')
    expect(screen.getByRole('checkbox')).toBeDisabled(); await user.click(screen.getByLabelText(copy.field.caCert)); await user.paste(importPublicCertificate)
    expect(screen.getByRole('checkbox')).toBeEnabled(); await confirm(user); await screen.findByText(copy.status.succeeded)
    expect(env.starts[0].supplements).toEqual([{ candidateId: candidate.candidateId, database: 'fixture_db', sslMode: 'VERIFY_IDENTITY', caCert: importPublicCertificate }])
  })
  it('existing fields get no fill-in entry, and a missing secret is not presented as importable from this computer', async () => {
    const user = userEvent.setup(); setup(importScan({ candidates: [importCandidate({ status: 'needs_fields', missingFields: ['password'], supplementableFields: [] })] })); render(); await discover(user)
    expect(screen.getByRole('checkbox')).toBeDisabled(); expect(screen.queryByRole('textbox')).toBeNull(); expect(screen.getByText(copy.missingSecret)).toBeInTheDocument()
  })
  it('public CA rejects private keys and garbage; disabled TLS with a CA is invalid, and no TLS default is guessed', () => {
    expect(publicCertificatePEM(importPublicCertificate)).toBe(true); expect(publicCertificatePEM(importPublicCertificate+'garbage')).toBe(false)
    expect(publicCertificatePEM('-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----')).toBe(false)
    const c = importCandidate({ cli: 'mysql', supplementableFields: ['sslMode', 'caCert'] })
    expect(supplementProblem(c, { sslMode: 'DISABLED', caCert: importPublicCertificate })).toBe(copy.invalidTLS)
    expect(supplementProblem(c, { sslMode: 'PREFERRED' })).toBe(copy.invalidTLS)
    expect(supplementProblem(importCandidate(), { database: 'override' })).toBe(copy.errors.supplement_override)
  })
  it.each(['unsupported', 'unreadable', 'requires_platform_login'] as const)('%s candidate is shown clearly and cannot be selected', async status => {
    const user = userEvent.setup(); setup(importScan({ candidates: [importCandidate({ status })] })); render(); await discover(user)
    expect(screen.getByRole('checkbox')).toBeDisabled(); expect(screen.getByText(copy.candidate[status])).toBeInTheDocument()
  })
  it('a previously imported source only warns about the risk; it is not merged by name or checked automatically', async () => {
    const user = userEvent.setup(); setup(importScan({ candidates: [importCandidate({ previouslyImported: true }), importCandidate({ candidateId: '51000000-0000-7000-8000-000000000002', previouslyImported: true })] })); render(); await user.click(screen.getByRole('button', { name: copy.entry })); await user.click(screen.getByRole('button', { name: copy.scan }))
    await screen.findAllByRole('checkbox'); screen.getAllByRole('checkbox').forEach(c => expect(c).not.toBeChecked()); expect(screen.getAllByText(copy.previous)).toHaveLength(2)
  })
  it('an unknown submit keeps the original ID and only recovers with GET; no new batch or rescan happens automatically', async () => {
    const user = userEvent.setup(), env = setup(); let id = '', count = 0
    server.use(http.post(apiUrl(`${base}/imports`), async ({ request }) => { count++; id = (await request.json() as Schemas['CLIImportStart']).operationId; env.set(importOperation({ id, status: 'succeeded', phase: null, credentialsRevision: 6, completedAt: '2026-10-16T10:00:10Z' })); return HttpResponse.json(errorBody('internal_error'), { status: 502 }) }))
    const view = render(); await discover(user); await confirm(user); await screen.findByText(copy.status.succeeded)
    expect(count).toBe(1); expect(env.scanCalls()).toBe(1); expect(env.reads).toContain(id); expect(view.router.state.location.search).toContain(id)
  })
  it('refreshing an operation deep link only GETs the original operation, without scanning or importing', async () => {
    const env = setup(); render(defaultProject.id, `/?cliImport=${importOperation().id}`)
    await screen.findByText(copy.status.running); expect(env.reads).toContain(importOperation().id); expect(env.scanCalls()).toBe(0); expect(env.starts).toHaveLength(0)
  })
  it('a failed batch shows the specific failure and the uncommitted items, and does not falsely report a partial save', async () => {
    const user = userEvent.setup(), env = setup()
    server.use(http.post(apiUrl(`${base}/imports`), async ({ request }) => { const id = (await request.json() as Schemas['CLIImportStart']).operationId; const op = importOperation({ id, status: 'failed', phase: null, completedAt: '2026-10-16T10:00:10Z', errorCode: 'source_changed', items: [{ ...importOperation().items[0], status: 'failed', errorCode: 'source_changed' }, { ...importOperation().items[0], candidateId: '51000000-0000-7000-8000-000000000002', status: 'not_committed' }] }); env.set(op); return HttpResponse.json({ operation: op }, { status: 202 }) }))
    render(); await discover(user); await confirm(user); await screen.findByText(copy.status.failed); expect(screen.getByText(copy.item.not_committed)).toBeInTheDocument(); expect(screen.queryByText(copy.item.imported)).toBeNull()
  })
  it('after cancelling, an old running GET does not overwrite the terminal state', async () => {
    const user = userEvent.setup(), env = setup(), op = importOperation(); env.set(op)
    server.use(http.post(apiUrl(`${base}/imports/${op.id}/cancel`), async ({ request }) => { expect(await request.json()).toEqual({}); return HttpResponse.json({ operation: { ...op, status: 'cancelled', phase: null, completedAt: '2026-10-16T10:00:10Z' } }) }))
    const view = render(defaultProject.id, `/?cliImport=${op.id}`); await screen.findByText(copy.status.running); await user.click(screen.getByRole('button', { name: copy.cancel })); await screen.findByText(copy.status.cancelled)
    await act(() => view.client.invalidateQueries({ queryKey: importKey(defaultProject.id, op.id) })); expect(screen.getByText(copy.status.cancelled)).toBeInTheDocument()
  })
  it.each(['import_scan_expired','import_source_changed','precondition_failed'] as const)('%s rejection does not rescan or resend automatically and keeps the selection', async code => {
    const user = userEvent.setup(), env = setup(); let calls = 0
    server.use(http.post(apiUrl(`${base}/imports`), () => { calls++; return HttpResponse.json(errorBody(code), { status: code === 'import_scan_expired' ? 409 : 412 }) }))
    // After the rejection, removing the operation ID from the URL is a router transition update and may
    // render one pass later than the error message, so wait for the back button to appear before clicking it.
    render(); await discover(user); await confirm(user); await screen.findByRole('alert'); expect(calls).toBe(1); expect(env.scanCalls()).toBe(1); await user.click(await screen.findByRole('button', { name: copy.back })); expect(screen.getByRole('checkbox')).toBeChecked()
  })
  it('an expired scan cannot send Start; an unknown GET failure is not treated as uncommitted', async () => {
    const user = userEvent.setup(), env = setup(importScan({ expiresAt: '2020-01-01T00:00:00Z' })); render(); await discover(user)
    expect(screen.getByRole('checkbox')).toBeDisabled(); expect(env.starts).toHaveLength(0); expect(screen.getByText(copy.expired)).toBeInTheDocument()
  })
  it.each([403,404,503])('GET %d does not falsely report a succeeded or failed terminal state and does not rescan', async status => {
    const env = setup(); server.use(http.get(apiUrl(`${base}/imports/:id`), () => HttpResponse.json(errorBody(status === 404 ? 'not_found' : status === 503 ? 'internal_error' : 'forbidden'), { status })))
    render(defaultProject.id, `/?cliImport=${importOperation().id}`); await screen.findByRole('alert'); expect(screen.getByText(copy.unknown)).toBeInTheDocument(); expect(screen.queryByRole('button', { name: copy.clearInvalidId })).toBeNull(); expect(env.starts).toHaveLength(0); expect(env.scanCalls()).toBe(0)
  })
  it('a cancel that loses to the commit returns succeeded and cannot falsely report a rollback', async () => {
    const user = userEvent.setup(), env = setup(), op = importOperation(); env.set(op)
    server.use(http.post(apiUrl(`${base}/imports/${op.id}/cancel`), () => HttpResponse.json({ operation: { ...op, status: 'succeeded', phase: null, completedAt: '2026-10-16T10:00:10Z', credentialsRevision: 6, items: [{ ...op.items[0], status: 'imported', accountId: '54000000-0000-7000-8000-000000000001', versionId: '55000000-0000-7000-8000-000000000001' }] } })))
    render(defaultProject.id, `/?cliImport=${op.id}`); await screen.findByText(copy.status.running); await user.click(screen.getByRole('button', { name: copy.cancel })); await screen.findByText(copy.status.succeeded)
    expect(screen.queryByText(copy.status.cancelled)).toBeNull(); expect(screen.getByText(copy.item.imported)).toBeInTheDocument()
  })
  it('a late scan for A cannot clear the operation locator of B or carry A candidates into B', async () => {
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
  it('at most 20 items can be selected, and there is no automatic import of everything', async () => {
    const user = userEvent.setup(), env = setup(importScan({ candidates: Array.from({ length: 21 }, (_, i) => importCandidate({ candidateId: `51000000-0000-7000-8000-${String(i+1).padStart(12, '0')}`, label: `Candidate ${i+1}` })) })); render()
    await user.click(screen.getByRole('button', { name: copy.entry })); await user.click(screen.getByRole('button', { name: copy.scan })); const boxes = await screen.findAllByRole('checkbox')
    for (const box of boxes) await user.click(box)
    expect(screen.getByText(copy.max)).toBeInTheDocument(); expect(boxes.filter(box => (box as HTMLInputElement).checked)).toHaveLength(20); expect(env.starts).toHaveLength(0)
  })

  it.each(['malformed', '00000000-0000-0000-0000-000000000000'])('an invalid locator %s can be cleared explicitly and then scanned by hand; nothing is requested automatically', async locator => {
    const user = userEvent.setup(), env = setup(), view = render(defaultProject.id, `/?cliImport=${locator}`)
    expect(screen.getByText(copy.invalidId)).toBeInTheDocument(); expect(env.scanCalls()).toBe(0); expect(env.starts).toHaveLength(0); expect(env.reads).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: copy.clearInvalidId }))
    expect(view.router.state.location.search).toBe(''); expect(screen.getByRole('button', { name: copy.scan })).toBeEnabled(); expect(env.scanCalls()).toBe(0); expect(env.starts).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: copy.scan })); expect(await screen.findByRole('checkbox')).not.toBeChecked(); expect(env.scanCalls()).toBe(1); expect(env.starts).toHaveLength(0)
  })

  it('while a rescan is unfinished, the old confirmation and fill-ins are locked and no old Start is sent', async () => {
    const user = userEvent.setup(), candidate = importCandidate({ cli: 'mysql', status: 'needs_fields', missingFields: ['database'], supplementableFields: ['database'] }), env = setup(importScan({ candidates: [candidate] })); render(); await discover(user)
    await user.type(screen.getByLabelText(copy.field.database), 'original_db'); await user.click(screen.getByRole('checkbox')); await user.click(screen.getByRole('button', { name: copy.next }))
    let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve })
    server.use(http.post(apiUrl(`${base}/scans`), async () => { await gate; return HttpResponse.json(importScan({ candidates: [candidate], scanId: '52000000-0000-7000-8000-000000000002' }), { status: 201 }) }))
    await user.click(screen.getByRole('button', { name: copy.rescan })); expect(screen.getByRole('button', { name: copy.confirm })).toBeDisabled(); await user.click(screen.getByRole('button', { name: copy.confirm })); expect(env.starts).toHaveLength(0)
    await user.click(screen.getByRole('button', { name: copy.back })); expect(screen.getByLabelText(copy.field.database)).toBeDisabled(); expect(screen.getByRole('checkbox')).toBeDisabled()
    await act(async () => { release(); await gate }); await waitFor(() => expect(screen.getByLabelText(copy.field.database)).toBeEnabled()); expect(screen.getByRole('checkbox')).not.toBeChecked(); expect(env.starts).toHaveLength(0)
  })
  it('when the same project switches to a valid operation, a late scan must not clear the new locator', async () => {
    const user = userEvent.setup(), env = setup(), op = importOperation(); env.set(op)
    let release!: () => void; const gate = new Promise<void>(resolve => { release = resolve })
    server.use(http.post(apiUrl(`${base}/scans`), async () => { await gate; return HttpResponse.json(importScan(), { status: 201 }) }))
    const view = render(); await user.click(screen.getByRole('button', { name: copy.entry })); await user.click(screen.getByRole('button', { name: copy.scan }))
    await act(() => view.router.navigate(`/?cliImport=${op.id}`)); await screen.findByText(copy.status.running)
    await act(async () => { release(); await gate }); await waitFor(() => expect(screen.getByRole('button', { name: copy.cancel })).toBeEnabled())
    expect(view.router.state.location.search).toContain(op.id); expect(screen.queryByRole('checkbox')).toBeNull(); expect(env.starts).toHaveLength(0)
  })

  it('a late Start 4xx cannot clear the new operation locator of the same project or rebuild the batch', async () => {
    const user = userEvent.setup(), env = setup(), newOp = importOperation(); env.set(newOp)
    let release!: () => void, posts = 0; const gate = new Promise<void>(resolve => { release = resolve })
    server.use(http.post(apiUrl(`${base}/imports`), async () => { posts++; await gate; return HttpResponse.json(errorBody('import_operation_conflict'), { status: 409 }) }))
    const view = render(); await discover(user); await confirm(user); await waitFor(() => expect(posts).toBe(1))
    await act(() => view.router.navigate(`/?cliImport=${newOp.id}`)); expect(view.router.state.location.search).toContain(newOp.id)
    await act(async () => { release(); await gate }); await screen.findByText(copy.status.running)
    expect(view.router.state.location.search).toContain(newOp.id); expect(posts).toBe(1); expect(env.scanCalls()).toBe(1); expect(screen.queryByRole('checkbox')).toBeNull()
  })

  it('a valid uppercase UUID locator recovers 200 by the same canonical ID, with no scan or start', async () => {
    const env = setup(), op = importOperation({ id: '53abcdef-abcd-7abc-8def-abcdef123456' }); env.set(op); render(defaultProject.id, `/?cliImport=${op.id.toUpperCase()}`)
    await screen.findByText(copy.status.running); expect(env.reads).toContain(op.id); expect(env.scanCalls()).toBe(0); expect(env.starts).toHaveLength(0); expect(screen.queryByText(copy.unknown)).toBeNull()
  })

})
