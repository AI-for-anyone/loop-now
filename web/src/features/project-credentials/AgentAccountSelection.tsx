import { t } from '@/i18n'
import { larkAccountOptionLabel } from './lark-account-copy'
import { LarkAccountDetails } from './LarkAccountDetails'
import { useEffect, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Schemas } from '@/api/client'
import { credentialKeys, invalidateProjectCredentials, putSelection, readSelections, useCredentialSelections } from '@/api/project-credentials'
import { ErrorAlert } from '@/components/ErrorAlert'
import { WarningList } from '@/features/agent-config/CredentialStatus'
import { Section } from '@/features/agent-config/Section'
import { useScope } from '@/features/projects/scope-context'
import { useRuntimeLookup } from '@/lib/runtime'
import { projectSettingsPath } from '@/lib/scope'
import { AccountSelectionField } from './AccountSelectionField'
import { CredentialConflict } from './CredentialConflict'
import { accountStatus, selectionDescription } from './account-copy'
import { useCredentialDraft } from './useCredentialDraft'
import { AgentDBConnections } from './AgentDBConnections'
import { LocalLoginNote } from './LocalLoginNote'

export function ProjectModelSelection({ agent }: { agent: Schemas['Agent'] }) {
  const result = useCredentialSelections(agent.id)
  const synchronized = useSelectionSynchronization(agent.id, agent.configVersion, result.data?.configVersion)
  // Providers already opened stay mounted, so a remote model switch doesn't silently drop their separate drafts.
  const [providers, setProviders] = useState([agent.model.provider])
  const [known, setKnown] = useState<{ source: typeof result.data; values: Map<string, Schemas['CredentialSelection']> }>({ source: undefined, values: new Map() })
  if (result.data !== known.source) { const values = new Map(known.values); for (const item of result.data?.selections ?? []) if (item.purpose === 'model' && item.provider) values.set(item.provider, item); setKnown({ source: result.data, values }) }
  if (!providers.includes(agent.model.provider)) setProviders([...providers, agent.model.provider])
  // A runtime with its own login on this computer (Claude Code) has no project account to choose: don't show where it takes an account
  // from, just explain. Other providers opened earlier are kept as usual; a provider's accounts are shown once its runtime is known to
  // use the platform's accounts.
  const lookup = useRuntimeLookup()
  const runtime = lookup.get(agent.runtime)
  return <Section id="model-credential" title={t.credentials.agentSelection.title}>
    {runtime && <p className="text-[13.5px]">{runtime.capabilities.accounts === 'local' ? t.runtime.account(runtime.name) : t.credentials.agentSelection.intro}</p>}
    <ErrorAlert error={result.error ?? lookup.error} />
    {!synchronized && result.data && <p role="status" className="text-[13px] font-bold">{t.credentials.agentSelection.modelOutOfSync}</p>}
    {!result.data && !result.isError && <p role="status">{t.credentials.agentSelection.loading}</p>}
    {result.data && providers.map(provider => {
      if (lookup.forProvider(provider)?.capabilities.accounts !== 'managed') return null
      const selection = known.values.get(provider)
      return selection ? <SelectionEditor key={provider} agentId={agent.id} data={result.data} selection={selection} current={provider === agent.model.provider} synchronized={synchronized} /> : null
    })}
  </Section>
}

export function ProjectCliSelection({ agentId, configVersion, cli }: { agentId: string; configVersion: number; cli: string }) {
  if (cli === 'mysql' || cli === 'psql') return <AgentDBConnections agentId={agentId} configVersion={configVersion} cli={cli} />
  return <LegacyProjectCliSelection agentId={agentId} configVersion={configVersion} cli={cli} />
}

export function LegacyProjectCliSelection({ agentId, configVersion, cli }: { agentId: string; configVersion: number; cli: string }) {
  const result = useCredentialSelections(agentId)
  const synchronized = useSelectionSynchronization(agentId, configVersion, result.data?.configVersion)
  const selection = result.data?.selections.find(item => item.purpose === 'cli' && item.cli === cli)
  return <><ErrorAlert error={result.error} />{!synchronized && result.data && <p role="status" className="text-[13px] font-bold">{t.credentials.agentSelection.cliOutOfSync}</p>}{selection && result.data ? <SelectionEditor agentId={agentId} data={result.data} selection={selection} synchronized={synchronized} /> : <p className="text-[13px]">{t.credentials.agentSelection.cliLoading}</p>}</>
}

function useSelectionSynchronization(agentId: string, agentVersion: number, selectionVersion: number | undefined) {
  const client = useQueryClient()
  const synchronized = selectionVersion === agentVersion
  useEffect(() => {
    if (selectionVersion !== undefined && !synchronized) {
      void client.invalidateQueries({ queryKey: ['agent', agentId] })
      void client.invalidateQueries({ queryKey: credentialKeys.selections(agentId) })
    }
  }, [client, agentId, agentVersion, selectionVersion, synchronized])
  return synchronized
}

function SelectionEditor({ agentId, data, selection, current = true, synchronized }: { agentId: string; data: Schemas['CredentialSelections']; selection: Schemas['CredentialSelection']; current?: boolean; synchronized: boolean }) {
  const scope = useScope(), client = useQueryClient()
  const slot = selection.provider ?? selection.cli ?? ''
  const [session, setSession] = useState(0)
  const [observed, setObserved] = useState(data.configVersion)
  const [editing, setEditing] = useState(false)
  // A clean form follows the remote config; with a draft it keeps using the original version, and after a CAS conflict the person reviews
  // explicitly.
  if (data.configVersion !== observed) {
    setObserved(data.configVersion)
    if (!editing) setSession(v => v + 1)
  }
  if (scope.kind !== 'project') return null
  return <SelectionForm key={session} id={`${agentId}:${selection.purpose}:${slot}`} agentId={agentId} projectId={data.projectId} configVersion={data.configVersion} selection={selection} current={current} synchronized={synchronized} settingsPath={projectSettingsPath(scope.project)} onDirty={setEditing} onSaved={async () => { setEditing(false); await invalidateProjectCredentials(client, data.projectId); setSession(v => v + 1) }} />
}

function SelectionForm({ id, agentId, projectId, configVersion, selection, settingsPath, current, synchronized, onDirty, onSaved }: {
  id: string; agentId: string; projectId: string; configVersion: number; selection: Schemas['CredentialSelection']; settingsPath: string; current: boolean; synchronized: boolean; onDirty: (dirty: boolean) => void; onSaved: () => void
}) {
  const client = useQueryClient(), [initial] = useState(selection)
  const slot = initial.provider ?? initial.cli ?? ''
  const currentId = selection.selectedAccountId ?? selection.resolvedAccountId
  const currentAccount = selection.candidates.find(account => account.id === currentId)
  const form = useCredentialDraft<string | null>({ id, initial: initial.selectedAccountId, revision: configVersion, summarize: selectionDescription(initial), readLatest: async () => {
    const latest = await readSelections(agentId)
    const item = latest.selections.find(item => item.purpose === initial.purpose && (initial.purpose === 'model' ? item.provider === slot : item.cli === slot))
    return { revision: latest.configVersion, summary: item ? selectionDescription(item) : t.credentials.agentSelection.configRemoved, deleted: !item }
  }, save: async (accountId, expectedConfigVersion) => {
    const latest = await putSelection(agentId, initial.purpose === 'model' ? { purpose: 'model', provider: slot, accountId, expectedConfigVersion } : { purpose: 'cli', cli: slot, accountId, expectedConfigVersion })
    client.setQueryData(credentialKeys.selections(agentId), latest)
  }, onSaved })
  return <div className="grid min-w-0 gap-3">
    {!current && <p role="status" className="text-[13px] font-bold">{t.credentials.agentSelection.switched(slot)}</p>}
    <AccountSelectionField label={t.credentials.agentSelection.fieldLabel(slot)} description={selectionDescription(selection)} options={selection.candidates.map(account => ({ value: account.id, label: slot === 'lark-cli' ? larkAccountOptionLabel(account) : `${account.name} · ${accountStatus[account.status]}` }))} value={form.draft ?? ''} currentLabel={selection.mode === 'automatic' ? (selection.resolvedAccountId ? t.credentials.agentSelection.automaticCurrent(selection.candidates.find(account => account.id === selection.resolvedAccountId)?.name ?? selection.resolvedAccountId) : t.credentials.agentSelection.notChosen) : undefined} canRestore={selection.mode !== 'automatic'} restoreLabel={selection.purpose === 'cli' ? t.credentials.agentSelection.restoreCliAutomatic : undefined} pending={form.pending} disabled={form.conflict || !synchronized} dirty={form.dirty} settingsPath={settingsPath} onChange={value => { form.setDraft(value || null); onDirty(true) }} onSave={() => void form.submit()} onRestore={() => { form.setDraft(null); onDirty(true); void form.submit(null) }} />
    {slot === 'lark-cli' && currentAccount && <LarkAccountDetails account={currentAccount} showName={false} />}
    {currentId && <p className="min-w-0 rounded-input bg-white px-3 py-2 text-[13.5px] [overflow-wrap:anywhere]">{selection.mode === 'explicit' ? t.credentials.agentSelection.explicitLabel : t.credentials.agentSelection.automaticLabel}{t.credentials.agentSelection.labelSeparator}<span className="font-black">{currentAccount?.name ?? currentId}</span>{currentAccount && <span> · {accountStatus[currentAccount.status]}</span>}</p>}
    {selection.candidates.filter(account => account.id === selection.resolvedAccountId || account.id === selection.selectedAccountId).map(account => <WarningList key={account.id} warnings={account.template?.warnings ?? []} />)}
    {selection.purpose === 'model' && selection.localLogin && <LocalLoginNote state={selection.localLogin} />}
    {form.conflict && <CredentialConflict base={form.base.summary} latest={form.review} reading={form.reading} error={form.readError} onRefresh={() => void form.refresh()} onAdopt={form.adopt} />}
    {!form.conflict && <ErrorAlert error={form.error} />}
    {/* projectId only identifies the source; accounts are never derived from candidates of other projects. */}
    <span className="sr-only">{t.credentials.agentSelection.projectSourcePrefix}{projectId}</span>
  </div>
}
