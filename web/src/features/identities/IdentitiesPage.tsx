import { useState } from 'react'
import { PagePluginActions } from '@/features/plugins/PluginActions'
import { IdentityManager } from '@/features/plugins/IdentityManager'
import { Navigate, NavLink, useNavigate, useParams } from 'react-router'
import { KeyRoundIcon, PlusIcon } from 'lucide-react'
import { useQueries } from '@tanstack/react-query'
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
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet } from '@/components/Sheet'
import { StickyNote } from '@/components/StickyNote'

// 外部身份（改版“一张印出来的纸”第 4 步）：页头是共用的页面标题（旁边是身份个数与“新建”）；纸上左边是未删除的外部身份，
// 选中的一项是黄底、藏青描边加投影的一块；右边是所选身份的令牌与只读收件箱。打开 /identities 时选中第一个。
// 已删除的身份不在列表中，但可以从线索中的“已删除身份”链接进入详情，只显示只读收件箱与删除时间。窄屏时列表在上、详情在下。
export function IdentitiesPage() {
  const { identityId } = useParams()
  const identities = useIdentities()
  const [creating, setCreating] = useState(false)
  const list = identities.data?.identities ?? []
  const tokens = useQueries({ queries: list.map((identity) => identityTokensQuery(identity.id)) })

  return (
    <PageFrame>
      {/* 跳到第一个身份时页面不卸载，加载期间打开的对话框不会丢失。 */}
      {!identityId && list.length > 0 && <Navigate replace to={`/identities/${list[0].id}`} />}
      <PageHeader crumbs={[{ label: t.shell.global }]} title={t.identities.title}>
        {identities.data && <span>{t.identities.count(list.length)}</span>}
      </PageHeader>
      <Sheet className="mt-2 flex-1">
        <div className="flex min-h-0 flex-1 flex-col nav:flex-row">
          <aside
            aria-label={t.identities.list}
            className="flex shrink-0 flex-col gap-2 overflow-y-auto border-b-2 border-dashed border-ink/40 px-4 py-4 nav:w-[236px] nav:border-r-2 nav:border-b-0 nav:py-5"
          >
            {/* “新建”是本页唯一的荧光粉主按钮，放在列表顶上。 */}
            <Button variant="interrupt" size="xs" className="mb-1.5 self-start border-2 border-ink" onClick={() => setCreating(true)}>
              <PlusIcon />
              {t.identities.create}
            </Button>
            {identities.isPending && <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
            {identities.isError && <ErrorAlert error={identities.error} />}
            <ul className="grid grid-cols-[minmax(0,1fr)] gap-1.5">
              {list.map((identity, index) => {
                const active = tokens[index]?.data?.tokens.filter((token) => tokenState(token) === 'active').length
                return (
                  <li key={identity.id} className="min-w-0">
                    <NavLink
                      to={`/identities/${identity.id}`}
                      className={({ isActive }) =>
                        cn(
                          'mr-1 flex min-h-[46px] items-center gap-2.5 rounded-input border-2 px-3 py-1.5 text-ink outline-none focus-visible:shadow-focus',
                          isActive ? 'border-ink bg-riso-yellow shadow-[4px_4px_0_var(--ink)]' : 'border-transparent hover:bg-ink/[0.05]',
                        )
                      }
                    >
                      <KeyRoundIcon aria-hidden="true" className="size-4 shrink-0" strokeWidth={2.4} />
                      <span className="flex min-w-0 flex-1 flex-col leading-[1.35]">
                        <span className="truncate text-[14.5px] font-black">{identity.name}</span>
                        {active !== undefined && <span className="text-[11.5px] font-bold text-ink-500">{t.identities.activeTokens(active)}</span>}
                      </span>
                    </NavLink>
                  </li>
                )
              })}
            </ul>
          </aside>
          {/* 滚动区是定位容器：里面绝对定位的元素（如 sr-only 说明）随内容滚动，不会按未滚动时的位置把整页撑高。 */}
          <section className="relative min-h-0 min-w-0 flex-1 overflow-y-auto pt-6 pr-[clamp(14px,3vw,40px)] pb-16 pl-[clamp(14px,2vw,30px)]">
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
        </div>
      </Sheet>
      {creating && <CreateIdentityDialog onClose={() => setCreating(false)} />}
    </PageFrame>
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
      {/* 令牌只显示一次、拥有全部权限：黄色便签（改版通用规则第 8 条）。 */}
      {!deleted && (
        <StickyNote className="self-start">
          <span className="font-black">{t.identities.riskTitle}</span>
          {t.identities.riskBody}
        </StickyNote>
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
    // 改版：身份名是 ZCOOL 34px 加一笔黄色记号笔，下面是等宽的地址；右边是红色描边的“删除外部身份”。
    <div className="flex flex-wrap items-start gap-x-[18px] gap-y-2.5">
      <div className="flex min-w-0 flex-[1_1_320px] flex-col gap-2">
      <h2 className="riso-marker self-start px-1.5 font-display-cn text-[34px] leading-[1.2] font-normal [overflow-wrap:anywhere]">
        {identity.name}
      </h2>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <span className="font-mono text-[12.5px] text-ink-700">{identity.address}</span>
        <Button size="xs" variant="secondary" className="h-6 bg-riso-yellow px-3 text-[12px] hover:bg-riso-yellow" onClick={() => void copy()}>
          {copied ? t.common.copied : t.common.copy}
        </Button>
        {deleted && <DeletedMark label={t.identities.deleted} className="text-[12.5px] leading-6" />}
        <span className="text-[12.5px] font-bold text-ink-500">
          {deleted ? t.identities.deletedAt(identity.deletedAt!) : t.identities.createdAt(identity.createdAt)}
        </span>
      </div>
      </div>
      {!deleted && (
        <Button
          variant="secondary"
          size="xs"
          className="ml-auto rounded-full border-2 border-red-text bg-transparent px-4 text-red-text hover:bg-tint-red"
          onClick={() => setConfirming(true)}
        >
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
