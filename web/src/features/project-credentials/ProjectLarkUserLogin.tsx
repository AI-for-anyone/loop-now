import { useEffect, useRef, useState, type ReactNode } from 'react'
import { useInRouterContext } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError, type Schemas } from '@/api/client'
import { acceptCLIAuthState, cancelCLIAuth, cliAuthActive, cliAuthKeys, startCLIAuth, useCLIAuthOperation, useProjectCLIAuth } from '@/api/cli-auth'
import { invalidateProjectCredentials, useProjectAccounts } from '@/api/project-credentials'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { backendSignals } from '@/api/backend-signals'
import { larkLoginLocatorKey, useOriginalActionLocator } from '@/lib/useOriginalActionLocator'
import { LarkUserLogin } from './LarkUserLogin'
import { LarkAccountDetails } from './LarkAccountDetails'
import { larkAuthorizationUrl } from './lark-authorization'
import { useProjectCredentialsAvailable } from './project-availability'

const copy = t.credentials.lark

type LarkOperation = Extract<Schemas['CLIAuthOperation'], { cli: 'lark-cli' }>
type LarkStart = Extract<Schemas['CLIAuthStart'], { cli: 'lark-cli' }>
const invalid = () => new ApiError({ status: 502, code: 'unknown', serverMessage: copy.unsafeResponse })
function unsupported(error: unknown) {
  return error instanceof ApiError && (error.status === 404 && error.serverMessage === backendSignals.missingEndpoint || error.status === 422 && error.fields.some(field => field.field === 'cli'))
}
export function ProjectLarkUserLogin({ projectId, account, applicationSetup }: { projectId: string; account?: Schemas['ProjectCredentialAccount']; applicationSetup?: ReactNode }) {
  const routed = useInRouterContext()
  return routed ? <LarkLogin key={`${projectId}:${account?.id ?? 'new'}`} projectId={projectId} account={account} applicationSetup={applicationSetup} />
    : <div><Button disabled>{copy.start}</Button><p>{copy.routeRequired}</p></div>
}
function LarkLogin({ projectId, account, applicationSetup }: { projectId: string; account?: Schemas['ProjectCredentialAccount']; applicationSetup?: ReactNode }) {
  const available = useProjectCredentialsAvailable(), client = useQueryClient()
  const locator = useOriginalActionLocator(larkLoginLocatorKey(projectId, account?.id))
  const accounts = useProjectAccounts(projectId)
  const [postError, setPostError] = useState<unknown>(null), [cancelError, setCancelError] = useState<unknown>(null)
  const completed = useRef<string | null>(null)
  const list = useProjectCLIAuth(projectId, available, 'lark-cli')
  const target = account?.id ?? null
  const validate = (state: Schemas['CLIAuthState'], expectedId = locator.id) => {
    const op = state.operation
    if (op.projectId !== projectId || op.cli !== 'lark-cli' || op.id !== expectedId || op.targetAccountId !== target) throw invalid()
    if (state.account && (state.account.projectId !== projectId || state.account.purpose !== 'cli' || state.account.cli !== 'lark-cli' || state.account.id !== (op.status === 'succeeded' ? op.accountId : target))) throw invalid()
    if (op.status === 'succeeded' && (!state.account || !state.account.larkIdentity || !state.account.currentVersionId || state.account.authMethod !== 'oauth')) throw invalid()
  }
  const listUnsupported = unsupported(list.error)
  const detail = useCLIAuthOperation(projectId, 'lark-cli', locator.valid ? locator.id : null, available && !listUnsupported && locator.valid && !locator.pending, validate)
  const op: LarkOperation | undefined = detail.data?.operation.cli === 'lark-cli' && detail.data.operation.id === locator.id ? detail.data.operation : undefined
  const discovered = list.data?.operations.find(operation => operation.cli === 'lark-cli' && operation.targetAccountId === target)
  const adopt = locator.adopt
  useEffect(() => { if (!locator.id && discovered) adopt(discovered.id) }, [locator.id, discovered, adopt])
  useEffect(() => {
    if (op?.status !== 'succeeded' || completed.current === op.id) return
    completed.current = op.id
    void invalidateProjectCredentials(client, projectId)
  }, [op, client, projectId])
  const source = op?.sourceAccountId ? accounts.data?.accounts.find(a => a.id === op.sourceAccountId) : undefined
  const capturedSource = source?.revision === op?.sourceRevision && source?.currentVersionId === op?.sourceVersionId ? source : undefined
  const brand = account?.larkIdentity?.brand ?? account?.larkApplication?.brand ?? capturedSource?.larkApplication?.brand
  const url = op?.status === 'awaiting_authorization' && brand && larkAuthorizationUrl(op.authorizationUrl, brand) ? op.authorizationUrl : undefined
  const badURL = op?.status === 'awaiting_authorization' && !url
  const mismatch = !!op && op.targetAccountId !== target || !!accounts.data && accounts.data.projectId !== projectId
  const disabled = mismatch || !locator.valid || locator.pending || list.isPending || !!list.error || !!detail.error || !!accounts.error || badURL
  const noSupport = listUnsupported || unsupported(detail.error) || unsupported(postError)
  const recovery = locator.id && !locator.pending && (!op || detail.error) ? detail.error instanceof ApiError && detail.error.status === 404 && !noSupport ? 'not-found' : detail.isFetching ? 'loading' : 'unknown' : undefined
  const sources = accounts.data?.accounts.filter(a => a.projectId === projectId && a.purpose === 'cli' && a.cli === 'lark-cli' && a.authMethod !== 'oauth' && !!a.larkApplication && !!a.currentVersionId && a.status !== 'deleted') ?? []
  const begin = async (selected: Schemas['ProjectCredentialAccount']) => {
    setPostError(null)
    locator.begin(async operationId => {
      const body: LarkStart = { operationId, cli: 'lark-cli', authType: 'lark_user', sourceAccountId: selected.id, expectedSourceRevision: selected.revision }
      try {
        const state = await startCLIAuth(projectId, body)
        validate(state, operationId)
        if (state.operation.cli !== 'lark-cli' || state.operation.targetAccountId !== null || state.operation.sourceAccountId !== selected.id || state.operation.sourceRevision !== selected.revision || state.operation.sourceVersionId !== selected.currentVersionId) throw invalid()
        acceptCLIAuthState(client, projectId, 'lark-cli', operationId, state)
      } catch (error) {
        setPostError(error)
        if (error instanceof ApiError && error.status >= 400 && error.status < 500) locator.rejectBeforeAcceptance(operationId)
        throw error
      } finally { void list.refetch() }
    }, !!op && !cliAuthActive(op))
  }
  const cancel = async (originalId: string) => {
    setCancelError(null)
    try {
      const state = await cancelCLIAuth(projectId, originalId)
      validate(state)
      await client.cancelQueries({ queryKey: cliAuthKeys.operation(projectId, originalId, 'lark-cli') })
      acceptCLIAuthState(client, projectId, 'lark-cli', originalId, state)
      void list.refetch()
    } catch (error) { setCancelError(error); throw error }
  }
  const recover = async () => { const result = await detail.refetch(); if (result.error) throw result.error }
  const saved = detail.data?.account ?? (op?.status === 'succeeded' ? accounts.data?.accounts.find(a => a.id === op.accountId) : account)
  const operationSource = op?.sourceAccountId && op.sourceVersionId ? copy.fixedSource(op.sourceAccountId, op.sourceVersionId) : account?.larkApplication ? copy.appSummary(account.larkApplication.brand, account.larkApplication.appId) : undefined
  return <div className="grid min-w-0 gap-3">
    {account && <p role="status" className="text-sm">{copy.reloginUnavailable}</p>}
    {!locator.valid && <p role="alert">{copy.invalidLocator}</p>}
    <LarkUserLogin projectId={projectId} targetAccount={account} sources={sources} available={available && !noSupport} disabled={disabled} cancelDisabled={mismatch || !!detail.error || !!list.error} operationId={locator.id} phase={op?.status ?? (locator.pending ? 'pending' : undefined)} recovery={recovery} expiresAt={op?.expiresAt} operationSource={operationSource}
      sourceDescription={application => application.larkApplication && <p>{copy.appSummary(application.larkApplication.brand, application.larkApplication.appId)}</p>}
      authorizationReady={!!url} authorizationHref={url} userCode={op?.status === 'awaiting_authorization' ? op.userCode : null}
      error={noSupport ? undefined : mismatch || badURL ? invalid() : postError ?? locator.error ?? cancelError ?? detail.error ?? list.error ?? accounts.error}
      savedAccount={saved && <LarkAccountDetails account={saved} />} applicationSetup={applicationSetup}
      onStart={begin} onRecover={recover} onCancel={cancel} />
  </div>
}
