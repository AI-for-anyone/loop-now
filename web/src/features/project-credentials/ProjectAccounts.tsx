import { useInRouterContext, useSearchParams } from 'react-router'
import { githubLocatorKey } from './github-authorization'
import { larkLoginLocatorKey } from '@/lib/useOriginalActionLocator'
import { t } from '@/i18n'
import { ProjectLarkUserLogin } from './ProjectLarkUserLogin'
import { LarkAccountDetails } from './LarkAccountDetails'
import { LocalCLIImport } from './LocalCLIImport'
import { CLIConnectionVerification } from './CLIConnectionVerification'
import { useId, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import type { Schemas } from '@/api/client'
import { createProjectAccount, deleteProjectAccount, invalidateProjectCredentials, projectCredentialsUnsupported, readProjectAccount, renameProjectAccount, replaceProjectAccount, useCredentialImpact, useProjectAccounts } from '@/api/project-credentials'
import { useCliTemplates, useModelProviders } from '@/api/queries'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { Label } from '@/components/ui/label'
import { selectClass } from '@/lib/field'
import { CliToolStatus, WarningList } from '@/features/agent-config/CredentialStatus'
import { accountStatus, accountSummary } from './account-copy'
import { CredentialEditor, type CredentialInput } from './CredentialEditor'
import { ProjectLogin } from './ProjectLogin'
import { GoogleLogin } from './GoogleLogin'
import { GitHubLogin } from './GitHubLogin'
import { ProjectCredentialsAvailable, useProjectCredentialsAvailable } from './project-availability'

// 单次能力读取统一模型/CLI错误；已有表单在暂时不可用时保留，不转为其他凭证身份。
export function ProjectCredentialSettings({ projectId }: { projectId: string }) {
  const accounts = useProjectAccounts(projectId)
  const available = !!accounts.data && !accounts.error
  return <ProjectCredentialsAvailable value={available}>
    {!available && <AccountCard title={t.credentials.accounts.title}>
      {projectCredentialsUnsupported(accounts.error)
        ? <p role="alert" className="text-[13.5px] font-bold">{t.credentials.accounts.unsupported}</p>
        : accounts.error ? <ErrorAlert error={accounts.error} /> : <p role="status">{t.credentials.accounts.loading}</p>}
      <div className="flex flex-wrap gap-2"><Button disabled>{t.credentials.accounts.codexLogin}</Button><Button disabled>{t.credentials.accounts.addAccount}</Button>{accounts.error && <Button variant="secondary" disabled={accounts.isFetching} onClick={() => void accounts.refetch()}>{t.credentials.accounts.reload}</Button>}</div>
    </AccountCard>}
    {accounts.data && <fieldset disabled={!available} hidden={!available} style={{ display: available ? 'contents' : 'none' }}>
      <ProjectAccounts projectId={projectId} purpose="model" />
      <ProjectAccounts projectId={projectId} purpose="cli" />
    </fieldset>}
  </ProjectCredentialsAvailable>
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
  return <Accounts {...props} initialSlot={props.cli ?? (props.purpose === 'cli' && params.has(githubLocatorKey(props.projectId)) ? 'gh' : props.purpose === 'cli' && [...params.keys()].some(key => key.startsWith(larkLoginLocatorKey(props.projectId).replace(/new$/, ''))) ? 'lark-cli' : '')} />
}
function Accounts({ projectId, purpose, cli, initialSlot = cli ?? '' }: AccountsProps & { initialSlot?: string }) {
  const accounts = useProjectAccounts(projectId), templates = useCliTemplates(), providers = useModelProviders(), client = useQueryClient()
  const [slot, setSlot] = useState(initialSlot), [creating, setCreating] = useState(false)
  const [retained, setRetained] = useState<Record<string, Schemas['ProjectCredentialAccount']>>({})
  const id = useId()
  const provider = providers.data?.providers.find(item => item.id === slot)
  const template = templates.data?.templates.find(item => item.name === slot)
  const options = purpose === 'model' ? (providers.data?.providers ?? []).filter(item => item.authMethods.length > 0).map(item => ({ value: item.id, label: item.id })) : (templates.data?.templates ?? []).map(item => ({ value: item.name, label: item.name }))
  const live = accounts.data?.accounts.filter(account => account.purpose === purpose && (!cli || account.cli === cli)) ?? []
  // 远端删除不能把仍在编辑的秘密表单直接卸载；保留编辑拥有者，提交/GET再读安全tombstone。
  const list = [...live, ...Object.values(retained).filter(account => !live.some(item => item.id === account.id))]
  const saveNew = async (input: CredentialInput) => {
    await createProjectAccount(projectId, purpose === 'model' ? { purpose, provider: slot, name: input.name.trim(), credential: { type: 'api_key', key: input.key } } : { purpose, cli: slot, name: input.name.trim(), credential: { type: 'cli', fields: input.fields } })
    await invalidateProjectCredentials(client, projectId)
  }
  return <AccountCard title={purpose === 'model' ? t.credentials.accounts.modelTitle : t.credentials.accounts.cliTitle}>
    <p className="text-[13.5px]">{purpose === 'model' ? t.credentials.accounts.modelIntro : t.credentials.accounts.cliIntro}</p>
    <ErrorAlert error={accounts.error} />
    {!accounts.data && !accounts.isError && <p role="status">{t.credentials.accounts.loadingList}</p>}
    {accounts.data && list.length === 0 && <p className="text-[13.5px] text-ink-500">{t.credentials.accounts.empty}</p>}
    <ul className="grid min-w-0 gap-3">{list.map(account => <AccountRow key={account.id} account={account} missing={!live.some(item => item.id === account.id)} onEditing={editing => setRetained(previous => { const next = { ...previous }; if (editing) next[account.id] = account; else delete next[account.id]; return next })} template={templates.data?.templates.find(item => item.name === account.cli)} methods={providers.data?.providers.find(item => item.id === account.provider)?.authMethods ?? []} />)}</ul>
    {purpose === 'cli' && !cli && <LocalCLIImport projectId={projectId} />}
    <div className="grid min-w-0 gap-3 border-t border-ink/10 pt-4">
      <Label htmlFor={id}>{purpose === 'model' ? t.credentials.accounts.addModelLabel : t.credentials.accounts.addCliLabel}</Label>
      <select id={id} className={selectClass} disabled={creating || !!cli} value={slot} onChange={e => setSlot(e.target.value)}><option value="">{t.credentials.accounts.choose}</option>{options.filter(option => !cli || option.value === cli).map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>
      <ErrorAlert error={purpose === 'model' ? providers.error : templates.error} />
      {purpose === 'cli' && template && <CliToolStatus template={template} />}
      {slot && <AccountImpact projectId={projectId} purpose={purpose} slot={slot} />}
      {purpose === 'model' && provider?.authMethods.includes('oauth') && slot === 'openai-codex' && <ProjectLogin key={slot} projectId={projectId} />}
      {purpose === 'cli' && slot === 'gcloud' && template && <GoogleLogin key={`${projectId}:gcloud:new`} projectId={projectId} />}
      {purpose === 'cli' && slot === 'lark-cli' && template && <ProjectLarkUserLogin projectId={projectId} applicationSetup={<Button type="button" variant="secondary" disabled={creating} onClick={() => setCreating(true)}>{t.credentials.lark.prepareApplication}</Button>} />}
      {purpose === 'cli' && slot === 'gh' && template && <><p className="text-[13px]">{t.credentials.github.purpose}</p><GitHubLogin projectId={projectId} /><p className="text-[13px]">{t.credentials.github.patDescription}</p><a className="text-[13px] font-bold text-riso-blue underline" href="https://cli.github.com/manual/gh_auth_login" target="_blank" rel="noopener noreferrer">{t.credentials.github.patHelp}</a></>}
      {!creating && (purpose === 'model' ? provider?.authMethods.includes('api_key') : !!template) && <Button type="button" variant="secondary" className="justify-self-start" onClick={() => setCreating(true)}>{purpose === 'model' ? t.credentials.accounts.enterApiKey : slot === 'gcloud' ? t.credentials.accounts.enterServiceAccount : slot === 'gh' ? t.credentials.github.pat : slot === 'lark-cli' ? t.credentials.lark.botCreate : t.credentials.accounts.enterTemplate}</Button>}
      {creating && <CredentialEditor key={`${purpose}:${slot}`} id={`new:${projectId}:${purpose}:${slot}`} mode={purpose === 'model' ? 'create-key' : 'create-cli'} template={template} revision={0} summary={t.credentials.accounts.newAccount} readLatest={async () => ({ revision: 0, summary: t.credentials.accounts.newUnknown })} onSave={saveNew} onClose={() => setCreating(false)} />}
    </div>
  </AccountCard>
}

export function AccountImpact({ projectId, purpose, slot, accountId }: { projectId: string; purpose: Schemas['ProjectCredentialPurpose']; slot: string; accountId?: string }) {
  const available = useProjectCredentialsAvailable()
  const impact = useCredentialImpact(projectId, purpose, slot, accountId, available)
  return <div className="grid min-w-0 gap-2 rounded-input bg-tint-blue p-3 text-[13px]">
    <p className="font-bold">{t.credentials.accounts.impactTitle}</p>
    <ErrorAlert error={impact.error} />
    {!impact.data && !impact.isError && <p>{t.credentials.accounts.impactLoading}</p>}
    {impact.data && <><p>{impact.data.agents.length ? impact.data.agents.map(agent => t.credentials.accounts.impactAgent(agent.name, agent.mode === 'connection_set' ? t.credentials.accounts.modeConnectionSet : agent.mode === 'explicit' ? t.credentials.accounts.modeExplicit : agent.mode === 'automatic' ? t.credentials.accounts.modeAutomatic : t.credentials.accounts.modeUnknown)).join(t.credentials.accounts.listSeparator) : t.credentials.accounts.noUsers}{t.credentials.accounts.revisionPrefix}{impact.data.credentialsRevision}</p><p>{accountId ? t.credentials.accounts.replaceImpact : t.credentials.accounts.addImpact}</p></>}
    <p>{t.credentials.accounts.capturedNote}</p>
  </div>
}

function AccountRow({ account, template, methods, missing, onEditing }: { account: Schemas['ProjectCredentialAccount']; template?: Schemas['CliTemplate']; methods: Schemas['ModelProvider']['authMethods']; missing: boolean; onEditing: (editing: boolean) => void }) {
  const client = useQueryClient()
  const available = useProjectCredentialsAvailable()
  const [editing, setEditing] = useState<'rename' | 'replace' | null>(null), [deleteRevision, setDeleteRevision] = useState<number | null>(null)
  const slot = account.provider ?? account.cli ?? ''
  const save = async (input: CredentialInput, revision: number) => {
    if (editing === 'rename') await renameProjectAccount(account.projectId, account.id, input.name.trim(), revision)
    else await replaceProjectAccount(account.projectId, account.id, account.purpose === 'model' ? { type: 'api_key', key: input.key } : { type: 'cli', fields: input.fields }, revision)
    await invalidateProjectCredentials(client, account.projectId)
  }
  return <li className="grid min-w-0 gap-3 rounded-card bg-white p-4">
    {missing && <p role="status" className="text-[13px] font-bold text-red-text">{t.credentials.accounts.missingFromList}</p>}
    <div className="flex min-w-0 flex-wrap items-center gap-2"><h3 className="min-w-0 flex-[1_1_160px] font-black [overflow-wrap:anywhere]">{account.name}</h3><Badge tone={account.status === 'saved' || account.status === 'logged_in' ? 'success' : 'danger'}>{accountStatus[account.status]}</Badge></div>
    <p className="text-[13px] [overflow-wrap:anywhere]">{slot} · {account.authMethod === 'oauth' ? account.cli === 'gcloud' ? t.credentials.accounts.methodGoogleBrowser : t.credentials.accounts.methodBrowser : account.purpose === 'model' ? 'API Key' : account.cli === 'gcloud' ? t.credentials.accounts.methodServiceAccount : t.credentials.accounts.methodTemplate}{account.accountLabel ? ` · ${account.accountLabel}` : ''}{t.credentials.accounts.updatedPrefix}{account.updatedAt}</p>
    {account.cli === 'gh' && <GitHubIdentity account={account} />}
    {account.cli === 'lark-cli' && <LarkAccountDetails account={account} showName={false} />}
    {template && (account.cli === 'lark-cli' && account.authMethod === 'oauth' ? <p className="text-[13px]">{t.credentials.lark.executionSeparate}</p> : <CliToolStatus template={template} />)}
    {account.template && <><p className="text-[13px] [overflow-wrap:anywhere]">{template ? t.credentials.accounts.templateVersion(template.version) : t.credentials.accounts.templateUnavailable}{t.credentials.accounts.sourcePrefix}{account.template.source}{t.credentials.accounts.formatPrefix}{account.template.credentialFormat}{t.credentials.accounts.templateStatusPrefix}{account.template.status}{t.credentials.accounts.fieldsPrefix}{account.template.fieldNames.join(t.credentials.accounts.fieldSeparator)}</p><WarningList warnings={account.template.warnings} /></>}
    <div className="flex flex-wrap gap-2"><Button type="button" size="sm" variant="ghost" disabled={!!editing || missing} onClick={() => { onEditing(true); setEditing('rename') }}>{t.credentials.accounts.rename}</Button>{(account.purpose === 'cli' ? !!template : methods.includes('api_key')) && <Button type="button" size="sm" variant="secondary" disabled={!!editing || missing} onClick={() => { onEditing(true); setEditing('replace') }}>{t.credentials.accounts.replace}</Button>}<Button type="button" size="sm" variant="ghost" disabled={missing} onClick={() => setDeleteRevision(account.revision)}>{t.credentials.accounts.delete}</Button></div>
    {!missing && <CLIConnectionVerification account={account} />}
    {account.purpose === 'model' && account.provider === 'openai-codex' && methods.includes('oauth') && <ProjectLogin projectId={account.projectId} account={account} />}
    {account.purpose === 'cli' && account.cli === 'gcloud' && !missing && <GoogleLogin key={`${account.projectId}:${account.id}`} projectId={account.projectId} account={account} />}
    {account.purpose === 'cli' && account.cli === 'gh' && !missing && <GitHubLogin projectId={account.projectId} account={account} />}
    {account.cli === 'lark-cli' && account.authMethod === 'oauth' && !missing && <ProjectLarkUserLogin projectId={account.projectId} account={account} />}
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

function AccountCard({ title, children }: { title: string; children: ReactNode }) {
  const id = useId()
  return <section aria-labelledby={id} className="grid min-w-0 gap-4 rounded-panel bg-paper-raised px-6 py-[22px] text-ink shadow-print-rest"><h2 id={id} className="font-display-cn text-[30px] leading-[1.15]">{title}</h2>{children}</section>
}
