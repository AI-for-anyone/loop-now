import { useState, type CSSProperties } from 'react'
import { PagePluginActions } from '@/features/plugins/PluginActions'
import { IdentityManager } from '@/features/plugins/IdentityManager'
import { Navigate, NavLink, useNavigate, useParams } from 'react-router'
import { PlusIcon } from 'lucide-react'
import { useQueries } from '@tanstack/react-query'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import type { Schemas } from '@/api/client'
import { identityTokensQuery, useDeleteIdentity, useIdentities, useIdentity } from '@/api/inbox'
import { DeletedMark } from '@/features/inbox/party'
import { IdentityConversation } from '@/features/console-inbox/IdentityConversation'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { cn } from '@/lib/utils'
import { CreateIdentityDialog } from './CreateIdentityDialog'
import { IdentityInbox } from './IdentityInbox'
import { tokenState } from './identity-rules'
import { TokenSection } from './TokenSection'
import { pageTitleClass } from '@/lib/page-title'

const tilts = ['-1deg', '0.8deg', '-0.6deg', '1.1deg']

// 外部身份：左侧是未删除的外部身份，右侧是所选身份的令牌与只读收件箱。打开 /identities 时选中第一个。
// 已删除的身份不在列表中，但可以从线索中的“已删除身份”链接进入详情，只显示只读收件箱与删除时间。
export function IdentitiesPage() {
  const { identityId } = useParams()
  const identities = useIdentities()
  const [creating, setCreating] = useState(false)
  const list = identities.data?.identities ?? []
  const tokens = useQueries({ queries: list.map((identity) => identityTokensQuery(identity.id)) })

  return (
    <div className="flex h-full flex-col lg:flex-row lg:gap-9 lg:pl-[30px]">
      {/* 跳到第一个身份时页面不卸载，加载期间打开的对话框不会丢失。 */}
      {!identityId && list.length > 0 && <Navigate replace to={`/identities/${list[0].id}`} />}
      <aside aria-label={t.identities.list} className="flex shrink-0 flex-col gap-4 px-4 pt-6 pb-4 sm:px-[30px] lg:w-[260px] lg:px-0 lg:pt-7">
        <div className="flex items-center justify-between gap-3">
          <h1 className={pageTitleClass}>
            {t.identities.title}
            {identities.data && (
              <span aria-hidden="true" className="ml-2 font-display-latin text-[18px] font-extrabold text-ink-500 [text-shadow:none]">
                {list.length}
              </span>
            )}
          </h1>
          <Button variant="ink" size="xs" onClick={() => setCreating(true)}>
            <PlusIcon />
            {t.identities.create}
          </Button>
        </div>
        {identities.isPending && <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
        {identities.isError && <ErrorAlert error={identities.error} />}
        <ul className="grid grid-cols-[minmax(0,1fr)] gap-3">
          {list.map((identity, index) => {
            const active = tokens[index]?.data?.tokens.filter((token) => tokenState(token) === 'active').length
            return (
              <li key={identity.id} className="min-w-0">
                <NavLink
                  to={`/identities/${identity.id}`}
                  style={({ isActive }) => ({ '--tilt': isActive ? tilts[index % tilts.length] : '0deg' }) as CSSProperties}
                  className={({ isActive }) =>
                    cn(
                      'riso-tilt flex min-h-[58px] items-center gap-3 rounded-card px-3.5 py-2.5 text-ink',
                      isActive && '[--tilt-bg:var(--riso-yellow)] [--tilt-shadow:var(--print-pink)]',
                    )
                  }
                >
                  <span
                    aria-hidden="true"
                    className="inline-flex size-9 shrink-0 items-center justify-center rounded-full bg-ink font-mono text-base font-bold text-riso-yellow shadow-[6px_2px_0_var(--riso-pink)]"
                  >
                    @
                  </span>
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate font-mono text-[14.5px] font-bold">{identity.name}</span>
                    {active !== undefined && <span className="text-[12px] font-bold text-ink-500">{t.identities.activeTokens(active)}</span>}
                  </span>
                </NavLink>
              </li>
            )
          })}
        </ul>
      </aside>
      <section className="min-h-0 min-w-0 flex-1 overflow-y-auto px-4 pt-2 pb-16 sm:px-[30px] lg:pt-7 lg:pl-0">
        {identityId ? (
          <IdentityDetail key={identityId} identityId={identityId} />
        ) : (
          identities.isSuccess && (
            <div className="flex min-h-[400px] items-center justify-center">
              <p className="font-display-cn text-[34px] text-riso-blue">{t.identities.empty}</p>
            </div>
          )
        )}
      </section>
      {creating && <CreateIdentityDialog onClose={() => setCreating(false)} />}
    </div>
  )
}

function IdentityDetail({ identityId }: { identityId: string }) {
  const identity = useIdentity(identityId)
  if (isNotFound(identity.error)) return <p className="pt-6 text-[15px] text-ink-500">{t.identities.notFound}</p>
  if (!identity.data) return identity.isError ? <ErrorAlert error={identity.error} /> : <p className="pt-6 text-[15px] text-ink-500">{t.common.loading}</p>
  const deleted = identity.data.deletedAt !== null
  return (
    <div className="flex max-w-[940px] flex-col gap-[22px]">
      <IdentityHeader identity={identity.data} />
      {!deleted && <PagePluginActions page="identity" where={{ identityId }} />}
      {!deleted && <IdentityManager identity={identity.data} />}
      {!deleted && (
        <Alert variant="warning" role="note">
          <AlertDescription className="text-[14px] leading-[1.75] text-ink">
            <span className="font-black">{t.identities.riskTitle}</span>
            {t.identities.riskBody}
          </AlertDescription>
        </Alert>
      )}
      {!deleted && <TokenSection identity={identity.data} />}
      <IdentityConversation identity={identity.data} />
      <IdentityInbox identityId={identityId} />
    </div>
  )
}

function IdentityHeader({ identity }: { identity: Schemas['ExternalIdentity'] }) {
  const navigate = useNavigate()
  const remove = useDeleteIdentity()
  const [confirming, setConfirming] = useState(false)
  const [copied, setCopied] = useState(false)
  const deleted = identity.deletedAt !== null
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(identity.address)
    } catch {
      return
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }
  return (
    <div className="flex flex-wrap items-end gap-x-[18px] gap-y-2.5">
      <h2 className="riso-misreg min-w-0 font-display-latin text-[48px] leading-none font-extrabold tracking-[-0.03em] [overflow-wrap:anywhere] text-riso-blue sm:text-[72px]">
        {identity.name}
      </h2>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 pb-2">
        <span className="font-mono text-[13px] text-ink-700">{identity.address}</span>
        <Button size="xs" variant="secondary" className="h-6 bg-riso-yellow px-3 text-[12px] hover:bg-riso-yellow" onClick={() => void copy()}>
          {copied ? t.common.copied : t.common.copy}
        </Button>
        {deleted && <DeletedMark label={t.identities.deleted} className="text-[12.5px] leading-6" />}
        <span className="text-[12.5px] font-bold text-ink-500">
          {deleted ? t.identities.deletedAt(identity.deletedAt!) : t.identities.createdAt(identity.createdAt)}
        </span>
      </div>
      {!deleted && (
        <Button variant="destructive-link" size="sm" className="mb-2 ml-auto" onClick={() => setConfirming(true)}>
          {t.identities.delete}
        </Button>
      )}
      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title={t.identities.deleteTitle(identity.name)}
        body={t.identities.deleteLead}
        confirmLabel={t.common.delete}
        destructive
        onConfirm={async () => {
          await remove.mutateAsync(identity.id)
          void navigate('/identities', { replace: true })
        }}
      >
        <ul className="m-0 flex list-disc flex-col gap-1 pl-5 text-[14.5px] leading-[1.7]">
          {t.identities.deleteConsequences.map((line) => (
            <li key={line}>{line}</li>
          ))}
        </ul>
      </ConfirmDialog>
    </div>
  )
}
