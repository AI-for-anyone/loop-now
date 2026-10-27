import { useInRouterContext, useSearchParams } from 'react-router'
import { githubLocatorKey } from './github-authorization'
import { larkLoginLocatorKey } from '@/lib/useOriginalActionLocator'
import { larkText } from '@/i18n/lark'
import { ProjectLarkUserLogin } from './ProjectLarkUserLogin'
import { LarkAccountDetails } from './LarkAccountDetails'
import { credentialText } from '@/i18n/credential-text'
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
import { githubLoginText } from '@/i18n/github-login'
import { ProjectCredentialsAvailable, useProjectCredentialsAvailable } from './project-availability'

// 单次能力读取统一模型/CLI错误；已有表单在暂时不可用时保留，不转为其他凭证身份。
export function ProjectCredentialSettings({ projectId }: { projectId: string }) {
  const accounts = useProjectAccounts(projectId)
  const available = !!accounts.data && !accounts.error
  return <ProjectCredentialsAvailable value={available}>
    {!available && <AccountCard title={credentialText.projectAccountsText1}>
      {projectCredentialsUnsupported(accounts.error)
        ? <p role="alert" className="text-[13.5px] font-bold">{credentialText.projectAccountsText2}</p>
        : accounts.error ? <ErrorAlert error={accounts.error} /> : <p role="status">{credentialText.projectAccountsText3}</p>}
      <div className="flex flex-wrap gap-2"><Button disabled>{credentialText.projectAccountsText4}</Button><Button disabled>{credentialText.projectAccountsText5}</Button>{accounts.error && <Button variant="secondary" disabled={accounts.isFetching} onClick={() => void accounts.refetch()}>{credentialText.projectAccountsText6}</Button>}</div>
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
  return <AccountCard title={purpose === 'model' ? credentialText.projectAccountsText7 : credentialText.projectAccountsText8}>
    <p className="text-[13.5px]">{purpose === 'model' ? credentialText.projectAccountsText9 : credentialText.projectAccountsText10}</p>
    <ErrorAlert error={accounts.error} />
    {!accounts.data && !accounts.isError && <p role="status">{credentialText.projectAccountsText11}</p>}
    {accounts.data && list.length === 0 && <p className="text-[13.5px] text-ink-500">{credentialText.projectAccountsText12}</p>}
    <ul className="grid min-w-0 gap-3">{list.map(account => <AccountRow key={account.id} account={account} missing={!live.some(item => item.id === account.id)} onEditing={editing => setRetained(previous => { const next = { ...previous }; if (editing) next[account.id] = account; else delete next[account.id]; return next })} template={templates.data?.templates.find(item => item.name === account.cli)} methods={providers.data?.providers.find(item => item.id === account.provider)?.authMethods ?? []} />)}</ul>
    {purpose === 'cli' && !cli && <LocalCLIImport projectId={projectId} />}
    <div className="grid min-w-0 gap-3 border-t border-ink/10 pt-4">
      <Label htmlFor={id}>{purpose === 'model' ? credentialText.projectAccountsText13 : credentialText.projectAccountsText14}</Label>
      <select id={id} className={selectClass} disabled={creating || !!cli} value={slot} onChange={e => setSlot(e.target.value)}><option value="">{credentialText.projectAccountsText15}</option>{options.filter(option => !cli || option.value === cli).map(option => <option key={option.value} value={option.value}>{option.label}</option>)}</select>
      <ErrorAlert error={purpose === 'model' ? providers.error : templates.error} />
      {purpose === 'cli' && template && <CliToolStatus template={template} />}
      {slot && <AccountImpact projectId={projectId} purpose={purpose} slot={slot} />}
      {purpose === 'model' && provider?.authMethods.includes('oauth') && slot === 'openai-codex' && <ProjectLogin key={slot} projectId={projectId} />}
      {purpose === 'cli' && slot === 'gcloud' && template && <GoogleLogin key={`${projectId}:gcloud:new`} projectId={projectId} />}
      {purpose === 'cli' && slot === 'lark-cli' && template && <ProjectLarkUserLogin projectId={projectId} applicationSetup={<Button type="button" variant="secondary" disabled={creating} onClick={() => setCreating(true)}>{larkText.prepareApplication}</Button>} />}
      {purpose === 'cli' && slot === 'gh' && template && <><p className="text-[13px]">{githubLoginText.purpose}</p><GitHubLogin projectId={projectId} /><p className="text-[13px]">{githubLoginText.patDescription}</p><a className="text-[13px] font-bold text-riso-blue underline" href="https://cli.github.com/manual/gh_auth_login" target="_blank" rel="noopener noreferrer">{githubLoginText.patHelp}</a></>}
      {!creating && (purpose === 'model' ? provider?.authMethods.includes('api_key') : !!template) && <Button type="button" variant="secondary" className="justify-self-start" onClick={() => setCreating(true)}>{purpose === 'model' ? credentialText.projectAccountsText16 : slot === 'gcloud' ? credentialText.projectAccountsText17 : slot === 'gh' ? githubLoginText.pat : slot === 'lark-cli' ? larkText.botCreate : credentialText.projectAccountsText18}</Button>}
      {creating && <CredentialEditor key={`${purpose}:${slot}`} id={`new:${projectId}:${purpose}:${slot}`} mode={purpose === 'model' ? 'create-key' : 'create-cli'} template={template} revision={0} summary={credentialText.projectAccountsText19} readLatest={async () => ({ revision: 0, summary: credentialText.projectAccountsText20 })} onSave={saveNew} onClose={() => setCreating(false)} />}
    </div>
  </AccountCard>
}

export function AccountImpact({ projectId, purpose, slot, accountId }: { projectId: string; purpose: Schemas['ProjectCredentialPurpose']; slot: string; accountId?: string }) {
  const available = useProjectCredentialsAvailable()
  const impact = useCredentialImpact(projectId, purpose, slot, accountId, available)
  return <div className="grid min-w-0 gap-2 rounded-input bg-tint-blue p-3 text-[13px]">
    <p className="font-bold">{credentialText.projectAccountsText21}</p>
    <ErrorAlert error={impact.error} />
    {!impact.data && !impact.isError && <p>{credentialText.projectAccountsText22}</p>}
    {impact.data && <><p>{impact.data.agents.length ? impact.data.agents.map(agent => credentialText.projectAccountsText23(agent.name, agent.mode === 'connection_set' ? credentialText.projectAccountsText24 : agent.mode === 'explicit' ? credentialText.projectAccountsText25 : agent.mode === 'automatic' ? credentialText.projectAccountsText26 : credentialText.projectAccountsText27)).join(credentialText.projectAccountsText28) : credentialText.projectAccountsText29}{credentialText.projectAccountsText30}{impact.data.credentialsRevision}</p><p>{accountId ? credentialText.projectAccountsText31 : credentialText.projectAccountsText32}</p></>}
    <p>{credentialText.projectAccountsText33}</p>
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
    {missing && <p role="status" className="text-[13px] font-bold text-red-text">{credentialText.projectAccountsText34}</p>}
    <div className="flex min-w-0 flex-wrap items-center gap-2"><h3 className="min-w-0 flex-[1_1_160px] font-black [overflow-wrap:anywhere]">{account.name}</h3><Badge tone={account.status === 'saved' || account.status === 'logged_in' ? 'success' : 'danger'}>{accountStatus[account.status]}</Badge></div>
    <p className="text-[13px] [overflow-wrap:anywhere]">{slot} · {account.authMethod === 'oauth' ? account.cli === 'gcloud' ? credentialText.projectAccountsText35 : credentialText.projectAccountsText36 : account.purpose === 'model' ? 'API Key' : account.cli === 'gcloud' ? credentialText.projectAccountsText37 : credentialText.projectAccountsText38}{account.accountLabel ? ` · ${account.accountLabel}` : ''}{credentialText.projectAccountsText39}{account.updatedAt}</p>
    {account.cli === 'gh' && <GitHubIdentity account={account} />}
    {account.cli === 'lark-cli' && <LarkAccountDetails account={account} showName={false} />}
    {template && (account.cli === 'lark-cli' && account.authMethod === 'oauth' ? <p className="text-[13px]">{larkText.executionSeparate}</p> : <CliToolStatus template={template} />)}
    {account.template && <><p className="text-[13px] [overflow-wrap:anywhere]">{template ? credentialText.projectAccountsText40(template.version) : credentialText.projectAccountsText41}{credentialText.projectAccountsText42}{account.template.source}{credentialText.projectAccountsText43}{account.template.credentialFormat}{credentialText.projectAccountsText44}{account.template.status}{credentialText.projectAccountsText45}{account.template.fieldNames.join(credentialText.projectAccountsText46)}</p><WarningList warnings={account.template.warnings} /></>}
    <div className="flex flex-wrap gap-2"><Button type="button" size="sm" variant="ghost" disabled={!!editing || missing} onClick={() => { onEditing(true); setEditing('rename') }}>{credentialText.projectAccountsText47}</Button>{(account.purpose === 'cli' ? !!template : methods.includes('api_key')) && <Button type="button" size="sm" variant="secondary" disabled={!!editing || missing} onClick={() => { onEditing(true); setEditing('replace') }}>{credentialText.projectAccountsText48}</Button>}<Button type="button" size="sm" variant="ghost" disabled={missing} onClick={() => setDeleteRevision(account.revision)}>{credentialText.projectAccountsText49}</Button></div>
    {!missing && <CLIConnectionVerification account={account} />}
    {account.purpose === 'model' && account.provider === 'openai-codex' && methods.includes('oauth') && <ProjectLogin projectId={account.projectId} account={account} />}
    {account.purpose === 'cli' && account.cli === 'gcloud' && !missing && <GoogleLogin key={`${account.projectId}:${account.id}`} projectId={account.projectId} account={account} />}
    {account.purpose === 'cli' && account.cli === 'gh' && !missing && <GitHubLogin projectId={account.projectId} account={account} />}
    {account.cli === 'lark-cli' && account.authMethod === 'oauth' && !missing && <ProjectLarkUserLogin projectId={account.projectId} account={account} />}
    {editing && <AccountImpact projectId={account.projectId} purpose={account.purpose} slot={slot} accountId={account.id} />}
    {editing === 'replace' && account.cli === 'gh' && <p className="text-[13px]">{githubLoginText.patReplace}</p>}
    {editing === 'replace' && account.cli === 'lark-cli' && account.authMethod === 'oauth' && <p className="text-[13px]">{larkText.staticReplace}</p>}
    {editing && <CredentialEditor key={editing} id={`account:${account.id}:${editing}`} name={account.name} mode={editing === 'rename' ? 'rename' : account.purpose === 'model' ? 'replace-key' : 'replace-cli'} template={template} revision={account.revision} summary={accountSummary(account)} readLatest={async () => { const latest = await readProjectAccount(account.projectId, account.id); return { revision: latest.revision, summary: accountSummary(latest), deleted: latest.status === 'deleted' } }} onSave={save} onClose={() => { setEditing(null); onEditing(false) }} />}
    <ConfirmDialog open={available && deleteRevision !== null} onOpenChange={open => { if (!open) setDeleteRevision(null) }} title={credentialText.projectAccountsText50(account.name)} body={credentialText.projectAccountsText51(deleteRevision ?? account.revision)} confirmLabel={credentialText.projectAccountsText52} destructive onConfirm={async () => { await deleteProjectAccount(account.projectId, account.id, deleteRevision!); await invalidateProjectCredentials(client, account.projectId) }}>
      {deleteRevision !== null && <AccountImpact projectId={account.projectId} purpose={account.purpose} slot={slot} accountId={account.id} />}
    </ConfirmDialog>
  </li>
}

function GitHubIdentity({ account }: { account: Schemas['ProjectCredentialAccount'] }) {
  const identity = account.githubIdentity
  if (!identity) return <p className="text-[13px]">{githubLoginText.identityMissing}</p>
  return <dl className="grid min-w-0 gap-2 text-[13px] [overflow-wrap:anywhere]">
    <div><dt className="font-bold">{githubLoginText.identity}</dt><dd>@{identity.login}</dd></div>
    <div><dt className="font-bold">{githubLoginText.identityId}</dt><dd>{identity.userId}</dd></div>
    <div><dt className="font-bold">{githubLoginText.granted}</dt><dd>{identity.grantedScopes === null ? githubLoginText.unknownScopes : identity.grantedScopes.length ? identity.grantedScopes.join(' · ') : githubLoginText.emptyScopes}</dd></div>
    <div><dt className="font-bold">{githubLoginText.verifiedAt}</dt><dd>{identity.verifiedAt}</dd></div>
    <div>{githubLoginText.permission}</div>
  </dl>
}

function AccountCard({ title, children }: { title: string; children: ReactNode }) {
  const id = useId()
  return <section aria-labelledby={id} className="grid min-w-0 gap-4 rounded-panel bg-paper-raised px-6 py-[22px] text-ink shadow-print-rest"><h2 id={id} className="font-display-cn text-[30px] leading-[1.15]">{title}</h2>{children}</section>
}
