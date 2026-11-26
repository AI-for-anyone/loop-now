import { useInRouterContext, useSearchParams } from 'react-router'
import { githubLocatorKey } from './github-authorization'
import { larkLoginLocatorKey } from '@/lib/useOriginalActionLocator'
import { t } from '@/i18n'
import { ProjectLarkUserLogin } from './ProjectLarkUserLogin'
import { LarkAccountDetails } from './LarkAccountDetails'
import { LocalCLIImport } from './LocalCLIImport'
import { CLIConnectionVerification } from './CLIConnectionVerification'
import { useContext, createContext, useId, useMemo, useRef, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Schemas } from '@/api/client'
import { cliAuthActive, useProjectCLIAuth } from '@/api/cli-auth'
import { useVerificationState, verificationActive, verificationUnsupported } from '@/api/cli-verification'
import { createProjectAccount, deleteProjectAccount, invalidateProjectCredentials, projectCredentialsUnsupported, projectLoginActive, readProjectAccount, renameProjectAccount, replaceProjectAccount, useCredentialImpact, useProjectAccounts, useProjectLogins } from '@/api/project-credentials'
import { useCliTemplates, useModelProviders } from '@/api/queries'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Label } from '@/components/ui/label'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { selectClass } from '@/lib/field'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { CliToolStatus, WarningList } from '@/features/agent-config/CredentialStatus'
import { accountStatus, accountSummary } from './account-copy'
import { CredentialEditor, type CredentialInput } from './CredentialEditor'
import { ProjectLogin } from './ProjectLogin'
import { GoogleLogin } from './GoogleLogin'
import { GitHubLogin } from './GitHubLogin'
import { ProjectCredentialsAvailable, useProjectCredentialsAvailable } from './project-availability'
import { providerEntries, providerName } from './provider-catalog'
import { AuthBadges, ProviderPicker } from './ProviderPicker'

const VERIFIABLE_CLIS = new Set(['gh', 'lark-cli', 'gcloud', 'psql', 'mysql'])

// Detects URL query parameters belonging to in-flight CLI credential recovery flows (local CLI import, GitHub device login, Lark user login).
function hasCredentialRecoveryParams(params: URLSearchParams, projectId: string): boolean {
  if (params.has('cliImport')) return true
  const ghPrefix = githubLocatorKey(projectId).replace(/new$/, '')
  const larkPrefix = larkLoginLocatorKey(projectId).replace(/new$/, '')
  for (const key of params.keys()) {
    if (key.startsWith(ghPrefix) || key.startsWith(larkPrefix)) return true
  }
  return false
}

// A single capability read covers the errors for both model and CLI; existing forms are kept while temporarily unavailable and are never
// switched to a different credential identity.
// embedded: in project settings it sits inside the Credentials tab with an inline segmented sub-filter between Model accounts and CLI accounts.
export function ProjectCredentialSettings({ projectId, embedded = false }: { projectId: string; embedded?: boolean }) {
  const routed = useInRouterContext()
  return routed
    ? <RoutedCredentialSettings projectId={projectId} embedded={embedded} />
    : <CredentialSettingsBody projectId={projectId} embedded={embedded} cliRecovery={false} />
}

function RoutedCredentialSettings({ projectId, embedded }: { projectId: string; embedded: boolean }) {
  const [params] = useSearchParams()
  const cliRecovery = hasCredentialRecoveryParams(params, projectId)
  return <CredentialSettingsBody projectId={projectId} embedded={embedded} cliRecovery={cliRecovery} />
}

function CredentialSettingsBody({ projectId, embedded, cliRecovery }: { projectId: string; embedded: boolean; cliRecovery: boolean }) {
  const accounts = useProjectAccounts(projectId)
  const available = !!accounts.data && !accounts.error
  const [override, setOverride] = useState<{ recovery: boolean; value: 'model' | 'cli' } | null>(null)
  const group: 'model' | 'cli' = override && override.recovery === cliRecovery ? override.value : cliRecovery ? 'cli' : 'model'

  const modelCount = accounts.data?.accounts.filter(account => account.purpose === 'model').length ?? 0
  const cliCount = accounts.data?.accounts.filter(account => account.purpose === 'cli').length ?? 0

  return <ProjectCredentialsAvailable value={available}><AccountsLayout.Provider value={embedded}>
    {!available && <AccountCard title={t.credentials.accounts.title} merged={embedded}>
      {projectCredentialsUnsupported(accounts.error)
        ? <p role="alert" className="text-[13.5px] font-medium [[data-skin=plate]_&]:font-bold">{t.credentials.accounts.unsupported}</p>
        : accounts.error ? <ErrorAlert error={accounts.error} /> : <p role="status">{t.credentials.accounts.loading}</p>}
      <div className="flex flex-wrap gap-2"><Button disabled>{t.credentials.accounts.browserLogin}</Button><Button disabled>{t.credentials.accounts.addAccount}</Button>{accounts.error && <Button variant="secondary" disabled={accounts.isFetching} onClick={() => void accounts.refetch()}>{t.credentials.accounts.reload}</Button>}</div>
    </AccountCard>}
    {accounts.data && <fieldset disabled={!available} hidden={!available} className={available ? 'grid min-w-0 gap-4' : 'hidden'}>
      <ToggleGroup
        aria-label={t.projects.settings.accounts.filterLabel}
        className={cn(segmentedClass, 'h-9 justify-self-start')}
        value={group}
        onValueChange={(value) => { if (value === 'model' || value === 'cli') setOverride({ recovery: cliRecovery, value }) }}
      >
        <ToggleGroupItem value="model" className={segmentedItemClass}>
          <span>{t.credentials.accounts.modelTitle}</span>
          <span className="ml-1.5 font-mono text-[11.5px] text-ink-500 tabular-nums">({modelCount})</span>
        </ToggleGroupItem>
        <ToggleGroupItem value="cli" className={segmentedItemClass}>
          <span>{t.credentials.accounts.cliTitle}</span>
          <span className="ml-1.5 font-mono text-[11.5px] text-ink-500 tabular-nums">({cliCount})</span>
        </ToggleGroupItem>
      </ToggleGroup>
      <div hidden={group !== 'model'} className={group === 'model' ? 'grid min-w-0 gap-4' : 'hidden'}>
        <ProjectAccounts projectId={projectId} purpose="model" />
      </div>
      <div hidden={group !== 'cli'} className={group === 'cli' ? 'grid min-w-0 gap-4' : 'hidden'}>
        <ProjectAccounts projectId={projectId} purpose="cli" />
      </div>
    </fieldset>}
  </AccountsLayout.Provider></ProjectCredentialsAvailable>
}

type AccountsProps = { projectId: string; purpose: Schemas['ProjectCredentialPurpose']; cli?: string }
export function ProjectAccounts(props: AccountsProps) {
  const routed = useInRouterContext()
  return routed ? <RoutedAccounts {...props} /> : <Accounts {...props} />
}
function RoutedAccounts(props: AccountsProps) {
  const [params] = useSearchParams()
  // Restore the owning UI before GET-only recovery; never infer an operation
  // from a secret or dispatch another POST during refresh.
  return <Accounts {...props} recoveryParams={params} initialSlot={props.cli ?? (props.purpose === 'cli' && params.has(githubLocatorKey(props.projectId)) ? 'gh' : props.purpose === 'cli' && [...params.keys()].some(key => key.startsWith(larkLoginLocatorKey(props.projectId).replace(/new$/, ''))) ? 'lark-cli' : '')} />
}
function Accounts({ projectId, purpose, cli, initialSlot = cli ?? '', recoveryParams }: AccountsProps & { initialSlot?: string; recoveryParams?: URLSearchParams }) {
  const accounts = useProjectAccounts(projectId), templates = useCliTemplates(), providers = useModelProviders(), client = useQueryClient()
  const [slot, setSlot] = useState(initialSlot), [creating, setCreating] = useState(false)
  const [retained, setRetained] = useState<Record<string, Schemas['ProjectCredentialAccount']>>({})
  const [picking, setPicking] = useState(false), saved = useRef(false)
  const id = useId()
  const provider = providers.data?.providers.find(item => item.id === slot)
  const template = templates.data?.templates.find(item => item.name === slot)
  const entries = useMemo(() => providerEntries((providers.data?.providers ?? []).filter(item => item.authMethods.length > 0)), [providers.data])
  const entry = entries.find(item => item.options.some(option => option.id === slot))
  const options = (templates.data?.templates ?? []).map(item => ({ value: item.name, label: item.name }))
  const live = accounts.data?.accounts.filter(account => account.purpose === purpose && (!cli || account.cli === cli)) ?? []
  // A remote delete must not unmount a secret form that is still being edited; keep the editing owner, and let the submit / a GET re-read
  // the safe tombstone.
  const list = [...live, ...Object.values(retained).filter(account => !live.some(item => item.id === account.id))]
  const saveNew = async (input: CredentialInput) => {
    await createProjectAccount(projectId, purpose === 'model' ? { purpose, provider: slot, name: input.name.trim(), credential: { type: 'api_key', key: input.key } } : { purpose, cli: slot, name: input.name.trim(), credential: { type: 'cli', fields: input.fields } })
    saved.current = true
    await invalidateProjectCredentials(client, projectId)
  }
  // When switching to a different provider, one that only takes a key opens the entry form directly; switching region variants of the same
  // provider leaves what was already filled in untouched.
  const pick = (next: string) => {
    const methods = providers.data?.providers.find(item => item.id === next)?.authMethods ?? []
    if (!entry?.options.some(option => option.id === next)) setCreating(methods.includes('api_key') && !methods.includes('oauth'))
    setSlot(next)
  }
  // After the model account entry form closes, go back to choosing a provider: reopen the panel on cancel, collapse it after a successful
  // save.
  const closeNew = () => {
    setCreating(false)
    if (purpose === 'model') { setSlot(''); setPicking(!saved.current) }
    saved.current = false
  }
  return <AccountCard title={purpose === 'model' ? t.credentials.accounts.modelTitle : t.credentials.accounts.cliTitle}>
    <p className="text-[13.5px]">{purpose === 'model' ? t.credentials.accounts.modelIntro : t.credentials.accounts.cliIntro}</p>
    <ErrorAlert error={accounts.error} />
    {!accounts.data && !accounts.isError && <p role="status">{t.credentials.accounts.loadingList}</p>}
    {accounts.data && list.length === 0 && <p className="text-[13.5px] text-ink-500">{t.credentials.accounts.empty}</p>}
    <ul className="grid min-w-0 gap-3">{list.map(account => {
      const hasRowRecovery = Boolean(
        recoveryParams &&
        ((account.cli === 'gh' && recoveryParams.has(githubLocatorKey(account.projectId, account.id))) ||
          (account.cli === 'lark-cli' && recoveryParams.has(larkLoginLocatorKey(account.projectId, account.id)))),
      )
      return <AccountRow key={account.id} account={account} missing={!live.some(item => item.id === account.id)} hasRecoveryLocator={hasRowRecovery} onEditing={editing => setRetained(previous => { const next = { ...previous }; if (editing) next[account.id] = account; else delete next[account.id]; return next })} template={templates.data?.templates.find(item => item.name === account.cli)} provider={providers.data?.providers.find(item => item.id === account.provider)} />
    })}</ul>
    {purpose === 'cli' && !cli && <LocalCLIImport projectId={projectId} />}
    <div className="grid min-w-0 gap-3 border-t border-hairline pt-4 [[data-skin=plate]_&]:border-ink/10">
      {purpose === 'model'
        ? <ProviderPicker label={t.credentials.accounts.addModelLabel} entries={entries} accounts={live} value={slot} open={picking} onOpenChange={setPicking} onChange={pick} />
        : <><Label htmlFor={id}>{t.credentials.accounts.addCliLabel}</Label>
          <select id={id} className={selectClass} disabled={creating || !!cli} value={slot} onChange={e => setSlot(e.target.value)}><option value="">{t.credentials.accounts.choose}</option>{options.filter(option => !cli || option.value === cli).map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select></>}
      <ErrorAlert error={purpose === 'model' ? providers.error : templates.error} />
      {/* When the panel is reopened to pick another provider, the form below is hidden first with its filled-in content kept; after Esc
          closes the panel, it comes back as it was. */}
      <div className="contents" hidden={picking}>
      {purpose === 'cli' && template && <CliToolStatus template={template} />}
      {slot && <AccountImpact projectId={projectId} purpose={purpose} slot={slot} />}
      {purpose === 'model' && provider?.authMethods.includes('oauth') && <ProjectLogin key={slot} projectId={projectId} provider={provider} />}
      {purpose === 'cli' && slot === 'gcloud' && template && <GoogleLogin key={`${projectId}:gcloud:new`} projectId={projectId} />}
      {purpose === 'cli' && slot === 'lark-cli' && template && <ProjectLarkUserLogin projectId={projectId} applicationSetup={<Button type="button" variant="secondary" disabled={creating} onClick={() => setCreating(true)}>{t.credentials.lark.prepareApplication}</Button>} />}
      {purpose === 'cli' && slot === 'gh' && template && <><p className="text-[13px]">{t.credentials.github.purpose}</p><GitHubLogin projectId={projectId} /><p className="text-[13px]">{t.credentials.github.patDescription}</p><a className="text-[13px] font-medium text-ink underline [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-riso-blue" href="https://cli.github.com/manual/gh_auth_login" target="_blank" rel="noopener noreferrer">{t.credentials.github.patHelp}</a></>}
      {!creating && (purpose === 'model' ? provider?.authMethods.includes('api_key') : !!template) && <Button type="button" variant="secondary" className="justify-self-start" onClick={() => setCreating(true)}>{purpose === 'model' ? t.credentials.accounts.enterApiKey : slot === 'gcloud' ? t.credentials.accounts.enterServiceAccount : slot === 'gh' ? t.credentials.github.pat : slot === 'lark-cli' ? t.credentials.lark.botCreate : t.credentials.accounts.enterTemplate}</Button>}
      {creating && <CredentialEditor key={`${purpose}:${entry?.key ?? slot}`} id={`new:${projectId}:${purpose}:${entry?.key ?? slot}`} mode={purpose === 'model' ? 'create-key' : 'create-cli'} template={template} revision={0} summary={t.credentials.accounts.newAccount} readLatest={async () => ({ revision: 0, summary: t.credentials.accounts.newUnknown })} onSave={saveNew} onClose={closeNew} />}
      </div>
    </div>
  </AccountCard>
}

export function AccountImpact({ projectId, purpose, slot, accountId }: { projectId: string; purpose: Schemas['ProjectCredentialPurpose']; slot: string; accountId?: string }) {
  const available = useProjectCredentialsAvailable()
  const impact = useCredentialImpact(projectId, purpose, slot, accountId, available)
  return <div className="grid min-w-0 gap-2 rounded-input border border-hairline bg-surface-subtle p-3 text-[13px] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue">
    <p className="font-semibold [[data-skin=plate]_&]:font-bold">{t.credentials.accounts.impactTitle}</p>
    <ErrorAlert error={impact.error} />
    {!impact.data && !impact.isError && <p>{t.credentials.accounts.impactLoading}</p>}
    {impact.data && <><p>{impact.data.agents.length ? impact.data.agents.map(agent => t.credentials.accounts.impactAgent(agent.name, agent.mode === 'connection_set' ? t.credentials.accounts.modeConnectionSet : agent.mode === 'explicit' ? t.credentials.accounts.modeExplicit : agent.mode === 'automatic' ? t.credentials.accounts.modeAutomatic : t.credentials.accounts.modeUnknown)).join(t.credentials.accounts.listSeparator) : t.credentials.accounts.noUsers}{t.credentials.accounts.revisionPrefix}{impact.data.credentialsRevision}</p><p>{accountId ? t.credentials.accounts.replaceImpact : t.credentials.accounts.addImpact}</p></>}
    <p>{t.credentials.accounts.capturedNote}</p>
  </div>
}

// provider: the model account's entry in the provider catalog (empty when the catalog isn't loaded or no longer has this provider).
function AccountRow({ account, template, provider, missing, hasRecoveryLocator, onEditing }: { account: Schemas['ProjectCredentialAccount']; template?: Schemas['CliTemplate']; provider?: Schemas['ModelProvider']; missing: boolean; hasRecoveryLocator: boolean; onEditing: (editing: boolean) => void }) {
  const methods = provider?.authMethods ?? []
  const client = useQueryClient()
  const available = useProjectCredentialsAvailable()
  const [editing, setEditing] = useState<'rename' | 'replace' | null>(null), [deleteRevision, setDeleteRevision] = useState<number | null>(null)
  const [verifyOpen, setVerifyOpen] = useState(false), [reloginOpen, setReloginOpen] = useState(false)
  const slot = account.provider ?? account.cli ?? ''

  const canVerify = !missing && account.purpose === 'cli' && !!account.cli && VERIFIABLE_CLIS.has(account.cli) && !!account.currentVersionId && account.status !== 'deleted'
  const reloginKind =
    account.purpose === 'model' && provider && methods.includes('oauth')
      ? 'model'
      : !missing && account.purpose === 'cli' && account.cli === 'gcloud'
        ? 'gcloud'
        : !missing && account.purpose === 'cli' && account.cli === 'gh'
          ? 'gh'
          : !missing && account.cli === 'lark-cli' && account.authMethod === 'oauth'
            ? 'lark'
            : null

  // Progressive disclosure: keep heavy verification and re-login sub-panels collapsed unless the user expands them, or an active operation,
  // error, or URL recovery locator targets this row.
  const verificationState = useVerificationState(account.projectId, account.id, account.currentVersionId ?? '', available && canVerify)
  const verificationOp = verificationState.data?.operation
  const verifyAutoOpen =
    canVerify &&
    ((!!verificationOp && (verificationActive(verificationOp) || verificationOp.status === 'failed')) ||
      (verificationState.isError && !verificationUnsupported(verificationState.error)))

  const modelLogins = useProjectLogins(account.projectId, available && reloginKind === 'model')
  const activeModelLogin =
    reloginKind === 'model' &&
    Boolean(modelLogins.data?.operations.some(op => op.projectId === account.projectId && op.provider === account.provider && op.targetAccountId === account.id && projectLoginActive(op)))

  const cliAuthFilter = reloginKind === 'gh' ? 'gh' : reloginKind === 'lark' ? 'lark-cli' : undefined
  const cliLogins = useProjectCLIAuth(account.projectId, available && (reloginKind === 'gcloud' || reloginKind === 'gh' || reloginKind === 'lark'), cliAuthFilter)
  const activeCliLogin =
    (reloginKind === 'gcloud' || reloginKind === 'gh' || reloginKind === 'lark') &&
    Boolean(cliLogins.data?.operations.some(op => op.projectId === account.projectId && op.cli === account.cli && op.targetAccountId === account.id && cliAuthActive(op)))

  const reloginAutoOpen = reloginKind !== null && (hasRecoveryLocator || activeModelLogin || activeCliLogin)
  const showVerify = canVerify && (verifyOpen || verifyAutoOpen)
  const showRelogin = reloginKind !== null && (reloginOpen || reloginAutoOpen)

  const save = async (input: CredentialInput, revision: number) => {
    if (editing === 'rename') await renameProjectAccount(account.projectId, account.id, input.name.trim(), revision)
    else await replaceProjectAccount(account.projectId, account.id, account.purpose === 'model' ? { type: 'api_key', key: input.key } : { type: 'cli', fields: input.fields }, revision)
    await invalidateProjectCredentials(client, account.projectId)
  }
  return <li className="grid min-w-0 gap-3 rounded-card border border-hairline bg-surface-control p-4 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-white">
    {missing && <p role="status" className="text-[13px] font-medium text-red-text [[data-skin=plate]_&]:font-bold">{t.credentials.accounts.missingFromList}</p>}
    <div className="flex min-w-0 flex-wrap items-center gap-2"><h3 className="min-w-0 flex-[1_1_160px] font-semibold [overflow-wrap:anywhere] [[data-skin=plate]_&]:font-black">{account.name}</h3><Badge tone={account.status === 'saved' || account.status === 'logged_in' ? 'success' : 'danger'}>{accountStatus[account.status]}</Badge></div>
    {account.purpose === 'model'
      ? <p className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-[13px] [overflow-wrap:anywhere]"><span className="font-semibold [[data-skin=plate]_&]:font-bold">{providerName(slot, provider)}</span>{providerName(slot, provider) !== slot && <span className="font-mono text-[12px] text-ink-500">{slot}</span>}<AuthBadges methods={[account.authMethod]} />{account.accountLabel && <span>{account.accountLabel}</span>}<span>{t.credentials.accounts.updated}<time dateTime={account.updatedAt}>{t.chat.time(account.updatedAt)}</time></span></p>
      : <p className="text-[13px] [overflow-wrap:anywhere]">{slot} · {account.authMethod === 'oauth' ? account.cli === 'gcloud' ? t.credentials.accounts.methodGoogleBrowser : t.credentials.accounts.methodBrowser : account.cli === 'gcloud' ? t.credentials.accounts.methodServiceAccount : t.credentials.accounts.methodTemplate}{account.accountLabel ? ` · ${account.accountLabel}` : ''}{t.credentials.accounts.updatedPrefix}<time dateTime={account.updatedAt}>{t.chat.time(account.updatedAt)}</time></p>}
    {account.cli === 'gh' && <GitHubIdentity account={account} />}
    {account.cli === 'lark-cli' && <LarkAccountDetails account={account} showName={false} />}
    {template && (account.cli === 'lark-cli' && account.authMethod === 'oauth' ? <p className="text-[13px]">{t.credentials.lark.executionSeparate}</p> : <CliToolStatus template={template} />)}
    {account.template && <><p className="text-[13px] [overflow-wrap:anywhere]">{template ? t.credentials.accounts.templateVersion(template.version) : t.credentials.accounts.templateUnavailable}{t.credentials.accounts.sourcePrefix}{account.template.source}{t.credentials.accounts.formatPrefix}{account.template.credentialFormat}{t.credentials.accounts.templateStatusPrefix}{t.credentials.accounts.templateStatuses[account.template.status] ?? account.template.status}{t.credentials.accounts.fieldsPrefix}{account.template.fieldNames.join(t.credentials.accounts.fieldSeparator)}</p><WarningList warnings={account.template.warnings} /></>}
    <div className="flex flex-wrap gap-2">
      <Button type="button" size="sm" variant="ghost" disabled={!!editing || missing} onClick={() => { onEditing(true); setEditing('rename') }}>{t.credentials.accounts.rename}</Button>
      {(account.purpose === 'cli' ? !!template : methods.includes('api_key')) && <Button type="button" size="sm" variant="secondary" disabled={!!editing || missing} onClick={() => { onEditing(true); setEditing('replace') }}>{t.credentials.accounts.replace}</Button>}
      {canVerify && <Button type="button" size="sm" variant="ghost" aria-expanded={showVerify} disabled={missing} onClick={() => setVerifyOpen(open => !open)}>{showVerify ? t.projects.settings.accounts.hideVerify : t.projects.settings.accounts.verify}</Button>}
      {reloginKind !== null && <Button type="button" size="sm" variant="ghost" aria-expanded={showRelogin} disabled={missing} onClick={() => setReloginOpen(open => !open)}>{showRelogin ? t.projects.settings.accounts.hideRelogin : t.projects.settings.accounts.relogin}</Button>}
      <Button type="button" size="sm" variant="ghost" disabled={missing} onClick={() => setDeleteRevision(account.revision)}>{t.credentials.accounts.delete}</Button>
    </div>
    {showVerify && <CLIConnectionVerification account={account} />}
    {showRelogin && reloginKind === 'model' && provider && <ProjectLogin projectId={account.projectId} provider={provider} account={account} />}
    {showRelogin && reloginKind === 'gcloud' && <GoogleLogin key={`${account.projectId}:${account.id}`} projectId={account.projectId} account={account} />}
    {showRelogin && reloginKind === 'gh' && <GitHubLogin projectId={account.projectId} account={account} />}
    {showRelogin && reloginKind === 'lark' && <ProjectLarkUserLogin projectId={account.projectId} account={account} />}
    {editing && <AccountImpact projectId={account.projectId} purpose={account.purpose} slot={slot} accountId={account.id} />}
    {editing === 'replace' && account.cli === 'gh' && <p className="text-[13px]">{t.credentials.github.patReplace}</p>}
    {editing === 'replace' && account.cli === 'lark-cli' && account.authMethod === 'oauth' && <p className="text-[13px]">{t.credentials.lark.staticReplace}</p>}
    {editing && <CredentialEditor key={editing} id={`account:${account.id}:${editing}`} name={account.name} mode={editing === 'rename' ? 'rename' : account.purpose === 'model' ? 'replace-key' : 'replace-cli'} template={template} revision={account.revision} summary={accountSummary(account)} readLatest={async () => { const latest = await readProjectAccount(account.projectId, account.id); return { revision: latest.revision, summary: accountSummary(latest), deleted: latest.status === 'deleted' } }} onSave={save} onClose={() => { setEditing(null); onEditing(false) }} />}
    <ConfirmDialog open={available && deleteRevision !== null} onOpenChange={open => { if (!open) setDeleteRevision(null) }} title={t.credentials.accounts.deleteTitle(account.name)} body={t.credentials.accounts.deleteBody(deleteRevision ?? account.revision)} confirmLabel={t.credentials.accounts.deleteConfirm} destructive onConfirm={async () => { await deleteProjectAccount(account.projectId, account.id, deleteRevision!); await invalidateProjectCredentials(client, account.projectId) }}>
      {deleteRevision !== null && <AccountImpact projectId={account.projectId} purpose={account.purpose} slot={slot} accountId={account.id} />}
    </ConfirmDialog>
  </li>
}

function GitHubIdentity({ account }: { account: Schemas['ProjectCredentialAccount'] }) {
  const identity = account.githubIdentity
  if (!identity) return <p className="text-[13px]">{t.credentials.github.identityMissing}</p>
  return <dl className="grid min-w-0 gap-2 text-[13px] [overflow-wrap:anywhere]">
    <div><dt className="font-bold">{t.credentials.github.identity}</dt><dd>@{identity.login}</dd></div>
    <div><dt className="font-bold">{t.credentials.github.identityId}</dt><dd>{identity.userId}</dd></div>
    <div><dt className="font-bold">{t.credentials.github.granted}</dt><dd>{identity.grantedScopes === null ? t.credentials.github.unknownScopes : identity.grantedScopes.length ? identity.grantedScopes.join(' · ') : t.credentials.github.emptyScopes}</dd></div>
    <div><dt className="font-bold">{t.credentials.github.verifiedAt}</dt><dd>{identity.verifiedAt}</dd></div>
    <div>{t.credentials.github.permission}</div>
  </dl>
}

// When embedded in project settings: not a card but a flat block inside the Credentials tab;
// merged: the content merges straight into the container (used for unavailable/loading states).
const AccountsLayout = createContext(false)
function AccountCard({ title, children, merged = false }: { title: string; children: ReactNode; merged?: boolean }) {
  const id = useId()
  const embedded = useContext(AccountsLayout)
  if (merged) return <div className="grid min-w-0 gap-4">{children}</div>
  if (embedded) return <section aria-labelledby={id} className="grid min-w-0 gap-3.5 text-ink"><h3 id={id} className="text-[17px] leading-[1.3] font-semibold [[data-skin=plate]_&]:font-black">{title}</h3>{children}</section>
  return <section aria-labelledby={id} className="grid min-w-0 gap-4 rounded-panel border border-hairline bg-surface-elevated px-6 py-[22px] text-ink shadow-surface [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-paper-raised [[data-skin=plate]_&]:shadow-print-rest"><h2 id={id} className="font-display-cn text-[24px] leading-[1.2] font-semibold [[data-skin=plate]_&]:text-[30px] [[data-skin=plate]_&]:leading-[1.15] [[data-skin=plate]_&]:font-normal">{title}</h2>{children}</section>
}
