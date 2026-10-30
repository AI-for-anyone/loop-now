import { useContext, useEffect, useId, useLayoutEffect, useRef, useState } from 'react'
import { useInRouterContext, useSearchParams } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError, type Schemas } from '@/api/client'
import { cancelCLIImport, importActive, importKey, importUnsupported, mergeImport, scanCLIImports, startCLIImport, useCLIImport, type ImportOperation } from '@/api/cli-import'
import { invalidateProjectCredentials } from '@/api/project-credentials'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { t } from '@/i18n'
import { selectClass } from '@/lib/field'
import { ScopeContext } from '@/features/projects/scope-context'
import { useDirtyForm } from '@/features/prompts/draft-navigation'
import { useProjectCredentialsAvailable } from './project-availability'
import { nonzeroUUID, selectedSupplements, supplementProblem, type SupplementDraft } from './import-selection'

const copy = t.credentials.cliImport

export function LocalCLIImport({ projectId, projectName }: { projectId: string; projectName?: string }) {
  const scope = useContext(ScopeContext)
  projectName ??= scope?.kind === 'project' && scope.project.id === projectId ? scope.project.name : undefined
  const routed = useInRouterContext()
  return routed ? <RoutedImport key={projectId} projectId={projectId} projectName={projectName} /> : <ImportEntry key={projectId} projectId={projectId} projectName={projectName} />
}
function RoutedImport({ projectId, projectName }: { projectId: string; projectName?: string }) {
  const [params, setParams] = useSearchParams()
  const locator = params.get('cliImport'), canonicalLocator = nonzeroUUID(locator) ? locator.toLowerCase() : locator
  return <ImportEntry projectId={projectId} projectName={projectName} recoveredId={canonicalLocator} locate={id => { const next = new URLSearchParams(params); if (id) next.set('cliImport', id); else next.delete('cliImport'); setParams(next, { replace: true }) }} />
}
function ImportEntry({ projectId, projectName, recoveredId = null, locate }: { projectId: string; projectName?: string; recoveredId?: string | null; locate?: (id: string | null) => void }) {
  const available = useProjectCredentialsAvailable()
  const [localId, setLocalId] = useState<string | null>(null), [open, setOpen] = useState(!!recoveredId)
  const operationId = locate ? recoveredId : localId
  return <div className="grid min-w-0 gap-3 border-t border-ink/10 pt-4">
    <Button type="button" variant="secondary" className="justify-self-start" disabled={!available} onClick={() => setOpen(value => !value)}>{open ? copy.close : copy.entry}</Button>
    {open && <ImportForm projectId={projectId} projectName={projectName} operationId={operationId} locate={id => { setLocalId(id); locate?.(id) }} />}
  </div>
}
function ImportForm({ projectId, projectName, operationId, locate }: { projectId: string; projectName?: string; operationId: string | null; locate: (id: string | null) => void }) {
  const available = useProjectCredentialsAvailable(), client = useQueryClient(), prefix = useId()
  const mounted = useRef(true), latestLocator = useRef(operationId), latestLocate = useRef(locate), scanEpoch = useRef(0), scanPending = useRef(false)
  useLayoutEffect(() => { latestLocator.current = operationId }, [operationId])
  useLayoutEffect(() => { latestLocate.current = locate }, [locate])
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const [now, setNow] = useState(Date.now)
  const [scan, setScan] = useState<Schemas['CLIImportScan'] | null>(null), [selected, setSelected] = useState<string[]>([]), [drafts, setDrafts] = useState<Record<string, SupplementDraft>>({})
  const [step, setStep] = useState<'select' | 'confirm'>('select'), [scanning, setScanning] = useState(false), [starting, setStarting] = useState(false), [cancelling, setCancelling] = useState(false)
  const [error, setError] = useState<unknown>(null), [errorSource, setErrorSource] = useState<'scan' | 'start' | 'cancel' | null>(null), [localError, setLocalError] = useState('')
  const validId = nonzeroUUID(operationId)
  const query = useCLIImport(projectId, validId ? operationId : null, available && !starting)
  const op = query.data?.id === operationId && query.data.projectId === projectId ? query.data : undefined
  const active = !!op && importActive(op), unresolved = !!operationId && !op
  const terminalId = op && !active ? op.id : null
  useEffect(() => { if (terminalId && op?.status === 'succeeded') void invalidateProjectCredentials(client, projectId) }, [terminalId, op?.status, client, projectId])
  useDirtyForm(`local-cli-import:${projectId}`, selected.length > 0 && !operationId)
  const problem = selected.some(id => { const candidate = scan?.candidates.find(c => c.candidateId === id); return !candidate || supplementProblem(candidate, drafts[id] ?? {}) !== null })
  useEffect(() => {
    if (!scan || operationId) return
    const timer = setInterval(() => { const next = Date.now(); setNow(next); if (next >= Date.parse(scan.expiresAt)) clearInterval(timer) }, 1000)
    return () => clearInterval(timer)
  }, [scan, operationId])
  const scanExpired = !!scan && now >= Date.parse(scan.expiresAt)
  const canChoose = available && !scanning && !starting && !active && !unresolved
  const discover = async () => {
    if (!canChoose || scanPending.current) return
    const epoch = ++scanEpoch.current, locatorAtStart = operationId
    scanPending.current = true
    setScanning(true); setErrorSource('scan'); setError(null); setLocalError('')
    try {
      const next = await scanCLIImports(projectId)
      if (!mounted.current || epoch !== scanEpoch.current || latestLocator.current !== locatorAtStart) return
      if (next.projectId !== projectId) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.mismatch })
      setNow(Date.now()); setScan(next); setSelected([]); setDrafts({}); setStep('select'); latestLocate.current(null)
    } catch (cause) { if (mounted.current && epoch === scanEpoch.current && latestLocator.current === locatorAtStart) setError(cause) }
    finally { if (epoch === scanEpoch.current) { scanPending.current = false; if (mounted.current) setScanning(false) } }
  }
  const store = (next: ImportOperation, id: string) => {
    if (next.projectId !== projectId || next.id !== id) throw new ApiError({ status: 502, code: 'unknown', serverMessage: copy.mismatch })
    client.setQueryData<ImportOperation>(importKey(projectId, id), old => mergeImport(old, next))
  }
  const start = async () => {
    if (scanPending.current || !canChoose || !scan || !selected.length || selected.length > 20 || problem) return
    if (Date.now() >= Date.parse(scan.expiresAt)) { setLocalError(copy.expired); return }
    const body: Schemas['CLIImportStart'] = { operationId: crypto.randomUUID(), scanId: scan.scanId, candidateIds: [...selected].sort(), expectedCredentialsRevision: scan.credentialsRevision, supplements: selectedSupplements(scan.candidates, selected, drafts) }
    setStarting(true); setErrorSource('start'); setError(null); setLocalError(''); latestLocator.current = body.operationId; latestLocate.current(body.operationId)
    try { const next = await startCLIImport(projectId, body); if (mounted.current && latestLocator.current === body.operationId) store(next, body.operationId) }
    catch (cause) {
      if (!mounted.current || latestLocator.current !== body.operationId) return
      setError(cause)
      if (cause instanceof ApiError && cause.status > 0 && cause.status < 500) latestLocate.current(null)
    } finally { if (mounted.current) setStarting(false) }
  }
  const cancel = async () => {
    if (!available || !op || !active || cancelling) return
    const id = op.id; setCancelling(true); setErrorSource('cancel'); setError(null)
    try {
      const next = await cancelCLIImport(projectId, id)
      if (!mounted.current || latestLocator.current !== id) return
      await client.cancelQueries({ queryKey: importKey(projectId, id) }); store(next, id)
    } catch (cause) { if (mounted.current && latestLocator.current === id) setError(cause) }
    finally { if (mounted.current) setCancelling(false) }
  }
  const toggle = (id: string) => {
    setLocalError('')
    if (selected.includes(id)) setSelected(old => old.filter(item => item !== id))
    else if (selected.length >= 20) setLocalError(copy.max)
    else setSelected(old => [...old, id])
  }
  return <section aria-label={copy.title} className="grid min-w-0 gap-4 rounded-card bg-tint-blue p-4">
    <h3 className="font-bold">{copy.title}</h3><p className="text-sm">{copy.intro}</p>
    <ol className="flex flex-wrap gap-2 text-sm font-bold">{copy.steps.map(label => <li key={label} className="rounded-input bg-white px-3 py-2">{label}</li>)}</ol>
    <p className="text-sm">{copy.scanHelp}</p>
    <Button type="button" variant="secondary" className="justify-self-start" disabled={!canChoose || scanning} onClick={() => void discover()}>{scanning ? copy.scanning : scan || op ? copy.rescan : copy.scan}</Button>
    {operationId && !validId && <div className="grid gap-2"><p role="alert">{copy.invalidId}</p><Button type="button" variant="secondary" className="justify-self-start" disabled={!available} onClick={() => { locate(null); setError(null); setLocalError('') }}>{copy.clearInvalidId}</Button></div>}
    {unresolved && validId && <p role="status">{starting ? copy.phase.preparing : copy.unknown}</p>}
    {query.isFetching && operationId && <p role="status">{copy.recovering}</p>}
    {importUnsupported(error ?? query.error) ? <p role="status">{copy.unsupported}</p> : <ErrorAlert error={query.error ?? (op ? null : error)} />}
    {localError && <p role="alert">{localError}</p>}
    {scan && !operationId && <>
      <p className="text-sm">{copy.deadline}{scan.expiresAt}</p>
      {scanExpired && <p role="status">{copy.expired}</p>}
      <ul className="flex flex-wrap gap-2 text-sm">{scan.sources.map(source => <li key={source.cli} className="rounded-input bg-white px-3 py-2">{source.cli} · {copy.sourceState[source.status]}{source.reasonCode ? ` · ${copy.reason[source.reasonCode]}` : ''}</li>)}</ul>
      {!scan.candidates.length && <p>{copy.empty}</p>}
      {step === 'select' ? <><ul className="grid min-w-0 gap-3">{scan.candidates.map(candidate => {
        const draft = drafts[candidate.candidateId] ?? {}, issue = supplementProblem(candidate, draft), checked = selected.includes(candidate.candidateId), id = `${prefix}-${candidate.candidateId}`
        return <li key={candidate.candidateId} className="grid min-w-0 gap-3 rounded-card bg-white p-4">
          <label className="flex min-w-0 items-start gap-3"><input type="checkbox" checked={checked} disabled={!canChoose || scanExpired || !!issue && !checked} onChange={() => toggle(candidate.candidateId)} className="mt-1 size-4 shrink-0 accent-riso-blue" /><span className="min-w-0 [overflow-wrap:anywhere]"><strong>{candidate.cli} · {candidate.label}</strong><span className="block text-sm">{copy.candidate[candidate.status]}</span></span></label>
          <p className="text-sm [overflow-wrap:anywhere]">{copy.source}{candidate.sourceKind}</p>
          {candidate.reasonCode && <p className="text-sm">{copy.reason[candidate.reasonCode]}</p>}
          {candidate.status === 'requires_platform_login' && <p className="text-sm">{copy.google}</p>}
          {candidate.previouslyImported && <p className="text-sm font-bold">{copy.previous}</p>}
          {candidate.missingFields.length > 0 && <p className="text-sm">{copy.missing}{candidate.missingFields.join(' · ')}</p>}
          {candidate.supplementableFields.length > 0 && <p className="text-sm">{copy.fieldsHelp}</p>}
          {candidate.supplementableFields.map(field => <div key={field} className="grid min-w-0 gap-1.5">
            <Label htmlFor={`${id}-${field}`}>{copy.field[field]}</Label>
            {field === 'sslMode' ? <select id={`${id}-${field}`} className={selectClass} value={draft[field] ?? ''} disabled={!canChoose} onChange={event => setDrafts(old => ({ ...old, [candidate.candidateId]: { ...draft, [field]: event.target.value } }))}><option value="">{copy.chooseTLS}</option>{(candidate.cli === 'mysql' ? ['DISABLED', 'REQUIRED', 'VERIFY_CA', 'VERIFY_IDENTITY'] : ['disable', 'require', 'verify-ca', 'verify-full']).map(mode => <option key={mode} value={mode}>{mode}</option>)}</select>
              : field === 'caCert' ? <><Textarea id={`${id}-${field}`} className="max-h-40 overflow-y-auto font-mono text-xs" rows={4} maxLength={65536} autoComplete="off" value={draft[field] ?? ''} disabled={!canChoose} onChange={event => setDrafts(old => ({ ...old, [candidate.candidateId]: { ...draft, [field]: event.target.value } }))} /><p className="text-xs">{copy.publicCA}</p></>
              : <Input id={`${id}-${field}`} maxLength={1024} value={draft[field] ?? ''} disabled={!canChoose} onChange={event => setDrafts(old => ({ ...old, [candidate.candidateId]: { ...draft, [field]: event.target.value } }))} />}
          </div>)}
          <FieldError message={candidate.status === 'may_import' || candidate.status === 'needs_fields' ? issue ?? undefined : undefined} />
        </li>
      })}</ul><p className="font-bold">{copy.selected}{selected.length}</p><Button type="button" className="justify-self-start" disabled={!canChoose || !selected.length || problem || scanExpired} onClick={() => setStep('confirm')}>{copy.next}</Button></>
        : <div className="grid gap-3 rounded-card bg-white p-4"><h4 className="font-bold [overflow-wrap:anywhere]">{copy.project}{projectName ?? projectId}</h4><p>{copy.selected}{selected.length}</p><ul>{scan.candidates.filter(c => selected.includes(c.candidateId)).map(c => <li key={c.candidateId} className="[overflow-wrap:anywhere]">{c.cli} · {c.label}</li>)}</ul><p className="text-sm">{copy.confirmHelp}</p><p className="text-sm">{copy.automatic}</p><p className="text-sm">{copy.savedOnly}</p><div className="flex flex-wrap gap-2"><Button type="button" variant="secondary" disabled={starting} onClick={() => setStep('select')}>{copy.back}</Button><Button type="button" disabled={!canChoose || problem || scanExpired} onClick={() => void start()}>{copy.confirm}</Button></div></div>}
    </>}
    {op && <div role="status" className="grid gap-3 rounded-card bg-white p-4">
      <h4 className="font-bold">{copy.status[op.status]}</h4>{op.phase && <p>{copy.phase[op.phase]}</p>}<p className="text-sm">{copy.operationDeadline}{op.deadlineAt}</p>
      {op.errorCode && <p>{copy.errors[op.errorCode]}</p>}
      <ul className="grid gap-2">{op.items.map(item => <li key={item.candidateId} className="grid min-w-0 gap-1 text-sm [overflow-wrap:anywhere]"><strong>{item.cli} · {item.label}</strong><span>{copy.item[item.status === 'imported' && op.status !== 'succeeded' ? 'not_committed' : item.status]}</span>{item.errorCode && <span>{copy.errors[item.errorCode]}</span>}{op.status === 'succeeded' && item.accountId && <details><summary>{copy.details}</summary><p>{copy.accountId}{item.accountId}</p><p>{copy.versionId}{item.versionId}</p></details>}</li>)}</ul>
      <p className="text-sm">{copy.savedOnly}</p><details><summary>{copy.details}</summary><p className="[overflow-wrap:anywhere]">{copy.operationId}{op.id}</p></details>
    </div>}
    {operationId && validId && <div className="flex flex-wrap gap-2"><Button type="button" variant="secondary" disabled={!available || query.isFetching || starting} onClick={() => void query.refetch()}>{copy.recover}</Button>{active && <Button type="button" variant="ghost" disabled={cancelling || !available} onClick={() => void cancel()}>{cancelling ? copy.cancelling : copy.cancel}</Button>}</div>}
    {op && errorSource !== 'start' && !!error && <ErrorAlert error={error} />}<p className="text-xs">{copy.closeHelp}</p>
  </section>
}
