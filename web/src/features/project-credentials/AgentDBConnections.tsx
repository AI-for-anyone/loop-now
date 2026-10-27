import { credentialText } from '@/i18n/credential-text'
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
  resolved: credentialText.agentDBConnectionsText1, not_configured: credentialText.agentDBConnectionsText2, selection_required: credentialText.agentDBConnectionsText3,
  account_deleted: credentialText.agentDBConnectionsText4, scope_mismatch: credentialText.agentDBConnectionsText5, unavailable: credentialText.agentDBConnectionsText6,
}
function summary(data: Schemas['DBConnections']) {
  return credentialText.agentDBConnectionsText7(data.mode === 'explicit_set' ? credentialText.agentDBConnectionsText8 : credentialText.agentDBConnectionsText9, statusText[data.status], data.configVersion, data.credentialsRevision, data.connections.map(item => `${item.name || item.accountId} · ${item.accountId} · ${statusText[item.status]}`).join('\n') || credentialText.agentDBConnectionsText10)
}

export function AgentDBConnections({ agentId, configVersion, cli }: { agentId: string; configVersion: number; cli: Schemas['DBConnectionCLI'] }) {
  const result = useDBConnections(agentId, cli), client = useQueryClient()
  const [dirty, setDirty] = useState(false), [session, setSession] = useState(0), [observed, setObserved] = useState('')
  const identity = result.data ? JSON.stringify([result.data.configVersion, result.data.mode, result.data.accountIds]) : ''
  const synchronized = result.data?.configVersion === configVersion
  useEffect(() => {
    if (result.data && !synchronized) void client.invalidateQueries({ queryKey: ['agent', agentId] })
  }, [client, agentId, synchronized, result.data])
  if (identity !== observed) { setObserved(identity); if (!dirty) setSession(value => value + 1) }
  return <div className="grid min-w-0 gap-3">
    <ErrorAlert error={result.error} />
    {!result.data && !result.isError && <p role="status">{credentialText.agentDBConnectionsText11}{cli}{credentialText.agentDBConnectionsText12}</p>}
    {result.data && <>
      <p className="text-[13.5px]">{credentialText.agentDBConnectionsText13}{result.data.mode === 'explicit_set' ? credentialText.agentDBConnectionsText14 : credentialText.agentDBConnectionsText15} · {statusText[result.data.status]}</p>
      {!synchronized && <p role="status">{credentialText.agentDBConnectionsText16}</p>}
      {result.data.mode === 'legacy_single' && <fieldset disabled={!synchronized || result.isError}><LegacyProjectCliSelection agentId={agentId} configVersion={configVersion} cli={cli} /></fieldset>}
      <ConnectionForm key={`${cli}:${session}`} data={result.data} synchronized={synchronized && !result.isError} onDirty={setDirty} onSaved={() => { setDirty(false); setSession(value => value + 1) }} />
    </>}
  </div>
}

// Only local editor state; HTTP bodies use the generated DBConnectionsPut schema.
type Draft = { action: 'set' | 'legacy'; ids: string[] }
function ConnectionForm({ data, synchronized, onDirty, onSaved }: { data: Schemas['DBConnections']; synchronized: boolean; onDirty: (dirty: boolean) => void; onSaved: () => void }) {
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
  return <div className="grid min-w-0 gap-3 rounded-card bg-tint-blue p-4 text-[13.5px]">
    <p className="font-bold">{data.cli}{credentialText.agentDBConnectionsText17}</p>
    {data.mode === 'explicit_set' && data.accountIds.length > 0 && data.status !== 'resolved' && <p role="status" className="rounded-input bg-tint-red p-3 font-bold text-red-text">{credentialText.agentDBConnectionsText18}</p>}
    <p>{credentialText.agentDBConnectionsText19}</p>
    <ErrorAlert error={accounts.error} />
    {!accounts.data && !accounts.isError && <p>{credentialText.agentDBConnectionsText20}</p>}
    <fieldset disabled={disabled} className="grid min-w-0 gap-2"><legend className="mb-2 font-bold">{credentialText.agentDBConnectionsText21}</legend>
      {ids.length === 0 && accounts.data && <p>{credentialText.agentDBConnectionsText22}</p>}
      {ids.map(id => {
        const candidate = candidates.find(item => item.id === id), connection = data.connections.find(item => item.accountId === id)
        return <label key={id} className={`flex min-w-0 items-start gap-3 rounded-input p-3 ${connection && connection.status !== 'resolved' ? 'bg-tint-red' : 'bg-white'}`}>
          <input type="checkbox" className="mt-1 size-4 shrink-0" checked={form.draft.ids.includes(id)} onChange={event => form.setDraft({ action: 'set', ids: (event.target.checked ? [...form.draft.ids, id] : form.draft.ids.filter(item => item !== id)).sort() })} />
          <span className="min-w-0 [overflow-wrap:anywhere]"><span className="font-bold">{candidate?.name ?? connection?.name ?? credentialText.agentDBConnectionsText23}</span><span className="block text-xs">{id}</span><span className="block">{connection ? statusText[connection.status] : candidate ? credentialText.agentDBConnectionsText24(accountStatus[candidate.status]) : credentialText.agentDBConnectionsText25}</span></span>
        </label>
      })}
    </fieldset>
    <p>{credentialText.agentDBConnectionsText26}</p>
    {form.conflict && <CredentialConflict base={form.base.summary} latest={form.review} reading={form.reading} error={form.readError} onRefresh={() => void form.refresh()} onAdopt={form.adopt} />}
    {!form.conflict && <ErrorAlert error={form.error} />}
    <div className="flex flex-wrap gap-2">
      <Button type="button" size="sm" disabled={disabled || (data.mode === 'explicit_set' && !form.dirty)} onClick={() => void form.submit()}>{form.pending ? credentialText.agentDBConnectionsText27 : form.draft.action === 'legacy' ? credentialText.agentDBConnectionsText28 : form.draft.ids.length ? credentialText.agentDBConnectionsText29 : credentialText.agentDBConnectionsText30}</Button>
      {data.mode === 'explicit_set' && <Button type="button" size="sm" variant="secondary" disabled={disabled} onClick={() => { const draft: Draft = { action: 'legacy', ids: form.draft.ids }; form.setDraft(draft); void form.submit(draft) }}>{credentialText.agentDBConnectionsText31}</Button>}
      {scope.kind === 'project' && <Link to={projectSettingsPath(scope.project)} className="inline-flex min-h-9 items-center font-bold underline">{credentialText.agentDBConnectionsText32}</Link>}
    </div>
    <p>{credentialText.agentDBConnectionsText33}</p>
  </div>
}
