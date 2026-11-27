import { t } from '@/i18n'
import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router'
import type { Schemas } from '@/api/client'
import { dbConnectionKey, deleteDBConnections, invalidateDBConnections, putDBConnections, readDBConnections, useDBConnections } from '@/api/db-connections'
import { credentialKeys, useProjectAccounts } from '@/api/project-credentials'
import { Button } from '@/components/ui/button'
import { ErrorAlert } from '@/components/ErrorAlert'
import { projectSettingsPath } from '@/lib/scope'
import { useScope } from '@/features/projects/scope-context'
import { LegacyProjectCliSelection } from './AgentAccountSelection'
import { CredentialConflict } from './CredentialConflict'
import { useCredentialDraft } from './useCredentialDraft'
import { accountStatus } from './account-copy'

const statusText: Record<Schemas['DBConnections']['status'], string> = {
  resolved: t.credentials.dbConnections.statusResolved, not_configured: t.credentials.dbConnections.statusNotConfigured, selection_required: t.credentials.dbConnections.statusSelectionRequired,
  account_deleted: t.credentials.dbConnections.statusAccountDeleted, scope_mismatch: t.credentials.dbConnections.statusScopeMismatch, unavailable: t.credentials.dbConnections.statusUnavailable,
}
function summary(data: Schemas['DBConnections']) {
  return t.credentials.dbConnections.summary(data.mode === 'explicit_set' ? t.credentials.dbConnections.modeExplicitSet : t.credentials.dbConnections.modeLegacy, statusText[data.status], data.configVersion, data.credentialsRevision, data.connections.map(item => `${item.name || item.accountId} · ${item.accountId} · ${statusText[item.status]}`).join('\n') || t.credentials.dbConnections.noResolved)
}

export function AgentDBConnections({ agentId, configVersion, cli, embedded = false }: { agentId: string; configVersion: number; cli: Schemas['DBConnectionCLI']; embedded?: boolean }) {
  const result = useDBConnections(agentId, cli), client = useQueryClient()
  const [dirty, setDirty] = useState(false), [session, setSession] = useState(0), [observed, setObserved] = useState('')
  const identity = result.data ? JSON.stringify([result.data.configVersion, result.data.mode, result.data.accountIds]) : ''
  const synchronized = result.data?.configVersion === configVersion
  useEffect(() => {
    if (result.data && !synchronized) void client.invalidateQueries({ queryKey: ['agent', agentId] })
  }, [client, agentId, synchronized, result.data])
  if (identity !== observed) { setObserved(identity); if (!dirty) setSession(value => value + 1) }
  return <div className="grid min-w-0 gap-2.5">
    <ErrorAlert error={result.error} />
    {!result.data && !result.isError && <p role="status" className="text-[13px] text-ink-500">{t.credentials.dbConnections.loading(cli)}</p>}
    {result.data && <>
      <p className="text-[13px]">{t.credentials.dbConnections.currentModeLabel}{result.data.mode === 'explicit_set' ? t.credentials.dbConnections.currentModeExplicitSet : t.credentials.dbConnections.currentModeLegacy} · {statusText[result.data.status]}</p>
      {!synchronized && <p role="status" className="text-[13px]">{t.credentials.dbConnections.outOfSync}</p>}
      {result.data.mode === 'legacy_single' && <fieldset disabled={!synchronized || result.isError}><LegacyProjectCliSelection agentId={agentId} configVersion={configVersion} cli={cli} embedded={embedded} /></fieldset>}
      <ConnectionForm key={`${cli}:${session}`} data={result.data} synchronized={synchronized && !result.isError} embedded={embedded} onDirty={setDirty} onSaved={() => { setDirty(false); setSession(value => value + 1) }} />
    </>}
  </div>
}

// Only local editor state; HTTP bodies use the generated DBConnectionsPut schema.
type Draft = { action: 'set' | 'legacy'; ids: string[] }
function ConnectionForm({ data, synchronized, embedded, onDirty, onSaved }: { data: Schemas['DBConnections']; synchronized: boolean; embedded: boolean; onDirty: (dirty: boolean) => void; onSaved: () => void }) {
  const client = useQueryClient(), scope = useScope()
  const accounts = useProjectAccounts(data.projectId)
  const form = useCredentialDraft<Draft>({
    id: `${data.agentId}:db-connections:${data.cli}`, initial: { action: 'set', ids: [...data.accountIds].sort() }, revision: data.configVersion,
    summarize: summary(data), readLatest: async () => { const latest = await readDBConnections(data.agentId, data.cli); return { revision: latest.configVersion, summary: summary(latest) } },
    save: async (draft, expectedConfigVersion) => {
      const latest = draft.action === 'legacy' ? await deleteDBConnections(data.agentId, data.cli, expectedConfigVersion) : await putDBConnections(data.agentId, data.cli, { accountIds: draft.ids, expectedConfigVersion })
      client.setQueryData(dbConnectionKey(data.agentId, data.cli), latest)
      await Promise.all([invalidateDBConnections(client, data.agentId), client.invalidateQueries({ queryKey: credentialKeys.selections(data.agentId) }), client.invalidateQueries({ queryKey: credentialKeys.project(data.projectId) })])
    }, onSaved,
  })
  useEffect(() => { onDirty(form.dirty || form.conflict) }, [onDirty, form.dirty, form.conflict])
  const candidates = accounts.data?.accounts.filter(account => account.purpose === 'cli' && account.cli === data.cli) ?? []
  const ids = [...new Set([...candidates.map(item => item.id), ...data.accountIds, ...form.draft.ids])].sort()
  const disabled = form.pending || form.conflict || !synchronized || !accounts.data || !!accounts.error
  return (
    <div
      className={
        embedded
          ? 'grid min-w-0 gap-2.5 text-[13px]'
          : 'grid min-w-0 gap-2.5 rounded-card border border-hairline bg-surface-subtle p-3.5 text-[13px]'
      }
    >
      <p className="font-semibold">{t.credentials.dbConnections.setTitle(data.cli)}</p>
      {data.mode === 'explicit_set' && data.accountIds.length > 0 && data.status !== 'resolved' && <p role="status" className="rounded-input border border-red/25 bg-tint-red p-2.5 font-medium text-red-text">{t.credentials.dbConnections.setBroken}</p>}
      <p className="text-ink-700">{t.credentials.dbConnections.intro}</p>
      <ErrorAlert error={accounts.error} />
      {!accounts.data && !accounts.isError && <p className="text-ink-500">{t.credentials.dbConnections.loadingCandidates}</p>}
      <fieldset disabled={disabled} className="grid min-w-0 gap-1.5"><legend className="mb-1.5 font-semibold">{t.credentials.dbConnections.chooseLegend}</legend>
        {ids.length === 0 && accounts.data && <p className="text-ink-500">{t.credentials.dbConnections.noCandidates}</p>}
        {ids.map(id => {
          const candidate = candidates.find(item => item.id === id), connection = data.connections.find(item => item.accountId === id)
          return <label key={id} className={`flex min-w-0 items-start gap-2.5 rounded-input p-2.5 ${connection && connection.status !== 'resolved' ? 'border border-red/25 bg-tint-red' : 'border border-hairline bg-surface-control'}`}>
            <input type="checkbox" className="mt-1 size-4 shrink-0 accent-ink" checked={form.draft.ids.includes(id)} onChange={event => form.setDraft({ action: 'set', ids: (event.target.checked ? [...form.draft.ids, id] : form.draft.ids.filter(item => item !== id)).sort() })} />
            <span className="min-w-0 [overflow-wrap:anywhere]"><span className="font-semibold">{candidate?.name ?? connection?.name ?? t.credentials.dbConnections.selectedUnavailable}</span><span className="block font-mono text-xs text-ink-500">{id}</span><span className="block text-[12.5px] text-ink-700">{connection ? statusText[connection.status] : candidate ? t.credentials.dbConnections.candidateNotGranted(accountStatus[candidate.status]) : t.credentials.dbConnections.missingAccount}</span></span>
          </label>
        })}
      </fieldset>
      <p className="text-[12.5px] text-ink-700">{t.credentials.dbConnections.multipleNote}</p>
      {form.conflict && <CredentialConflict base={form.base.summary} latest={form.review} reading={form.reading} error={form.readError} onRefresh={() => void form.refresh()} onAdopt={form.adopt} />}
      {!form.conflict && <ErrorAlert error={form.error} />}
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" size="sm" disabled={disabled || (data.mode === 'explicit_set' && !form.dirty)} onClick={() => void form.submit()}>{form.pending ? t.credentials.dbConnections.saving : form.draft.action === 'legacy' ? t.credentials.dbConnections.confirmLegacy : form.draft.ids.length ? t.credentials.dbConnections.saveSet : t.credentials.dbConnections.saveEmpty}</Button>
        {data.mode === 'explicit_set' && <Button type="button" size="sm" variant="secondary" disabled={disabled} onClick={() => { const draft: Draft = { action: 'legacy', ids: form.draft.ids }; form.setDraft(draft); void form.submit(draft) }}>{t.credentials.dbConnections.deleteSet}</Button>}
        {scope.kind === 'project' && <Link to={projectSettingsPath(scope.project, 'credentials')} className="inline-flex min-h-8 items-center text-[12.5px] font-medium underline underline-offset-4">{t.credentials.dbConnections.manageInSettings}</Link>}
      </div>
      <p className="text-[12.5px] text-ink-700">{t.credentials.dbConnections.deleteSetNote}</p>
    </div>
  )
}
