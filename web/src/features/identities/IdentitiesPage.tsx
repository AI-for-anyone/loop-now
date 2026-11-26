import { useRef, useState } from 'react'
import { Navigate, NavLink, useNavigate, useParams } from 'react-router'
import { KeyRoundIcon, PlusIcon } from 'lucide-react'
import { useQueries } from '@tanstack/react-query'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { ConfirmDialog } from '@/components/ConfirmDialog'
import { ErrorAlert } from '@/components/ErrorAlert'
import { SubNav, type SubNavLinkItem } from '@/components/IndexTabs'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { ScrollRuler } from '@/components/PrecisionRuler'
import { Sheet } from '@/components/Sheet'
import { StickyNote } from '@/components/StickyNote'
import type { Schemas } from '@/api/client'
import { identityTokensQuery, useDeleteIdentity, useIdentities, useIdentity } from '@/api/inbox'
import { IdentityConversation } from '@/features/console-inbox/IdentityConversation'
import { DeletedMark } from '@/features/inbox/party'
import { IdentityManager } from '@/features/plugins/IdentityManager'
import { PagePluginActions } from '@/features/plugins/PluginActions'
import { t } from '@/i18n'
import { isNotFound } from '@/lib/errors'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { CreateIdentityDialog } from './CreateIdentityDialog'
import { IdentityInbox } from './IdentityInbox'
import { tokenState } from './identity-rules'
import { TokenSection } from './TokenSection'

type IdentityTab = 'messages' | 'tokens' | 'settings'
type MessageMode = 'direct' | 'all'

function resolveTab(tab: string | undefined): IdentityTab {
  if (tab === 'tokens' || tab === 'settings') return tab
  return 'messages'
}

// External identities workspace: compact left master rail + right detail workspace with persistent compact identity header and
// 3 URL-synced SubNav tabs (Messages, Tokens & Access, Settings). Deleted identities show only Messages.
export function IdentitiesPage() {
  const { identityId, tab } = useParams()
  const activeTab = resolveTab(tab)
  const tabSuffix = activeTab === 'messages' ? '' : `/${activeTab}`
  const identities = useIdentities()
  const [creating, setCreating] = useState(false)
  const asideScrollRef = useRef<HTMLDivElement>(null)
  const list = identities.data?.identities ?? []
  const tokens = useQueries({ queries: list.map((identity) => identityTokensQuery(identity.id)) })

  const activeCounts = new Map<string, number | undefined>()
  list.forEach((identity, index) => {
    const data = tokens[index]?.data
    activeCounts.set(identity.id, data ? data.tokens.filter((token) => tokenState(token) === 'active').length : undefined)
  })

  return (
    <PageFrame>
      {/* Redirecting to the first identity doesn't unmount the page; skip while creating so post-create redirect to /tokens wins. */}
      {!creating && !identityId && list.length > 0 && <Navigate replace to={`/identities/${list[0].id}`} />}
      <PageHeader
        crumbs={[{ label: t.shell.global }]}
        title={t.identities.title}
        actions={
          <Button variant="interrupt" size="xs" className="[[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink" onClick={() => setCreating(true)}>
            <PlusIcon aria-hidden="true" />
            {t.identities.create}
          </Button>
        }
      >
        {identities.data && <span>{t.identities.count(list.length)}</span>}
      </PageHeader>
      <Sheet className="flex-1 [[data-skin=plate]_&]:mt-2">
        <div className="flex min-h-0 flex-1 flex-col nav:flex-row">
          <aside
            aria-label={t.identities.list}
            className="relative flex shrink-0 flex-col border-b border-hairline max-nav:max-h-[200px] nav:w-[216px] nav:border-r nav:border-b-0 [[data-skin=plate]_&]:border-b-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/40 [[data-skin=plate]_&]:nav:border-r-2 [[data-skin=plate]_&]:nav:border-b-0"
          >
            <div ref={asideScrollRef} className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto px-3 py-3 nav:py-4">
              {identities.isPending && <p className="text-[13.5px] text-ink-500">{t.common.loading}</p>}
              {identities.isError && <ErrorAlert error={identities.error} />}
              <ul className="grid grid-cols-[minmax(0,1fr)] gap-1">
                {list.map((identity) => {
                  const active = activeCounts.get(identity.id)
                  return (
                    <li key={identity.id} className="min-w-0">
                      <NavLink
                        to={`/identities/${identity.id}${tabSuffix}`}
                        className={({ isActive }) =>
                          cn(
                            'flex min-h-[44px] items-center gap-2.5 rounded-input border px-2.5 py-1.5 text-ink outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:mr-1 [[data-skin=plate]_&]:min-h-[46px] [[data-skin=plate]_&]:border-2',
                            isActive
                              ? 'border-hairline-strong bg-surface-active [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink)]'
                              : 'border-transparent hover:bg-surface-hover [[data-skin=plate]_&]:hover:bg-ink/[0.05]',
                          )
                        }
                      >
                        <KeyRoundIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.2} />
                        <span className="flex min-w-0 flex-1 flex-col leading-[1.35]">
                          <span className="truncate text-[13.5px] font-semibold [[data-skin=plate]_&]:text-[14px] [[data-skin=plate]_&]:font-black">
                            {identity.name}
                          </span>
                          {active !== undefined && (
                            <span className="text-[11px] font-medium text-ink-500 [[data-skin=plate]_&]:font-bold">
                              {t.identities.activeTokens(active)}
                            </span>
                          )}
                        </span>
                      </NavLink>
                    </li>
                  )
                })}
              </ul>
            </div>
            <ScrollRuler targetRef={asideScrollRef} label={t.identities.list} />
          </aside>
          <section className="flex min-h-0 min-w-0 flex-1 flex-col">
            {identityId ? (
              <IdentityDetail
                key={identityId}
                identityId={identityId}
                tab={activeTab}
                activeTokenCount={activeCounts.get(identityId)}
              />
            ) : (
              identities.isSuccess && (
                <div className="flex min-h-[320px] flex-1 items-center justify-center p-6">
                  <p className="font-display-cn text-[18px] font-semibold text-ink-500 [[data-skin=plate]_&]:text-[34px] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:text-riso-blue">
                    {t.identities.empty}
                  </p>
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

function IdentityDetail({
  identityId,
  tab,
  activeTokenCount,
}: {
  identityId: string
  tab: IdentityTab
  activeTokenCount?: number
}) {
  const identity = useIdentity(identityId)
  const bodyScrollRef = useRef<HTMLDivElement>(null)

  if (isNotFound(identity.error)) return <p className="p-6 text-[14.5px] text-ink-500">{t.identities.notFound}</p>
  if (!identity.data) {
    return (
      <div className="p-6">
        {identity.isError ? <ErrorAlert error={identity.error} /> : <p className="text-[14.5px] text-ink-500">{t.common.loading}</p>}
      </div>
    )
  }

  const data = identity.data
  const deleted = data.deletedAt !== null
  const effectiveTab: IdentityTab = deleted ? 'messages' : tab
  const navItems: SubNavLinkItem[] = deleted
    ? [{ key: 'messages', label: t.identities.tabs.messages, to: `/identities/${identityId}`, end: true }]
    : [
        { key: 'messages', label: t.identities.tabs.messages, to: `/identities/${identityId}`, end: true },
        { key: 'tokens', label: t.identities.tabs.tokens, to: `/identities/${identityId}/tokens`, count: activeTokenCount },
        { key: 'settings', label: t.identities.tabs.settings, to: `/identities/${identityId}/settings` },
      ]

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <IdentityHeader identity={data} />
      <SubNav aria-label={data.name} items={navItems} />
      <div className="relative flex min-h-0 flex-1 flex-col overflow-hidden">
        <div ref={bodyScrollRef} className="min-h-0 flex-1 overflow-y-auto px-5 py-4 sm:px-6">
          <div className="flex max-w-[940px] flex-col gap-5">
            {effectiveTab === 'messages' && <IdentityMessagesTab key={deleted ? 'deleted' : 'active'} identity={data} />}
            {effectiveTab === 'tokens' && !deleted && (
              <>
                <StickyNote className="self-start">
                  <span className="font-semibold [[data-skin=plate]_&]:font-black">{t.identities.riskTitle}</span>
                  {t.identities.riskBody}
                </StickyNote>
                <TokenSection identity={data} />
              </>
            )}
            {effectiveTab === 'settings' && !deleted && <IdentitySettingsTab identity={data} />}
          </div>
        </div>
        <ScrollRuler targetRef={bodyScrollRef} label={data.name} />
      </div>
    </div>
  )
}

function IdentityHeader({ identity }: { identity: Schemas['ExternalIdentity'] }) {
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
    <div className="flex shrink-0 flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-hairline px-5 py-3 sm:px-6 [[data-skin=plate]_&]:border-b-0 [[data-skin=plate]_&]:pb-2">
      <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1.5">
        <h2 className="riso-marker font-display-cn text-[18px] leading-snug font-semibold tracking-tight [overflow-wrap:anywhere] [[data-skin=plate]_&]:px-1.5 [[data-skin=plate]_&]:text-[28px] [[data-skin=plate]_&]:leading-[1.15] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal">
          {identity.name}
        </h2>
        <span className="font-mono text-[12.5px] text-ink-700">{identity.address}</span>
        <Button
          size="xs"
          variant="secondary"
          className="h-6 px-2.5 text-[11.5px] [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:hover:bg-riso-yellow"
          onClick={() => void copy()}
        >
          {copied ? t.common.copied : t.common.copy}
        </Button>
        {deleted ? (
          <>
            <DeletedMark label={t.identities.deleted} className="text-[12px] leading-5" />
            <span className="text-[12px] font-medium text-ink-500 [[data-skin=plate]_&]:font-bold">
              {t.identities.deletedAt(identity.deletedAt!)}
            </span>
          </>
        ) : (
          identity.managedBy && (
            <Badge variant="chip" tone="neutral">
              {t.identities.managedByPill(`${identity.managedBy.pluginId} · ${identity.managedBy.channelId}`)}
            </Badge>
          )
        )}
      </div>
      {!deleted && (
        <div className="ml-auto flex shrink-0 items-center gap-2">
          <PagePluginActions page="identity" where={{ identityId: identity.id }} />
        </div>
      )}
    </div>
  )
}

function IdentityMessagesTab({ identity }: { identity: Schemas['ExternalIdentity'] }) {
  const deleted = identity.deletedAt !== null
  const [mode, setMode] = useState<MessageMode>(deleted ? 'all' : 'direct')

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-center gap-3">
        <ToggleGroup
          aria-label={t.identities.messageModes.label}
          className={segmentedClass}
          value={mode}
          onValueChange={(value) => {
            if (value === 'direct' || value === 'all') setMode(value)
          }}
        >
          <ToggleGroupItem value="direct" className={segmentedItemClass}>
            {t.identities.messageModes.direct}
          </ToggleGroupItem>
          <ToggleGroupItem value="all" className={segmentedItemClass}>
            {t.identities.messageModes.all}
          </ToggleGroupItem>
        </ToggleGroup>
      </div>
      {mode === 'direct' ? <IdentityConversation identity={identity} /> : <IdentityInbox identityId={identity.id} />}
    </div>
  )
}

function IdentitySettingsTab({ identity }: { identity: Schemas['ExternalIdentity'] }) {
  const navigate = useNavigate()
  const remove = useDeleteIdentity()
  const [confirming, setConfirming] = useState(false)

  return (
    <div className="flex min-w-0 flex-col gap-6">
      <IdentityManager identity={identity} />
      <section
        aria-labelledby="identity-metadata-title"
        className="flex flex-col gap-3 border-t border-hairline pt-5 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/[0.22]"
      >
        <h3 id="identity-metadata-title" className="text-[15px] leading-[1.3] font-semibold [[data-skin=plate]_&]:text-[17px] [[data-skin=plate]_&]:font-black">
          {t.identities.metadata.title}
        </h3>
        <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-6 gap-y-2 text-[13.5px]">
          <dt className="font-medium text-ink-500 [[data-skin=plate]_&]:font-black">{t.identities.metadata.address}</dt>
          <dd className="font-mono text-[13px] text-ink">{identity.address}</dd>
          <dt className="font-medium text-ink-500 [[data-skin=plate]_&]:font-black">{t.identities.metadata.createdAt}</dt>
          <dd className="text-ink-700">{t.identities.createdAt(identity.createdAt)}</dd>
        </dl>
      </section>
      <section
        aria-labelledby="identity-danger-title"
        className="flex flex-wrap items-center justify-between gap-4 rounded-card border border-red-text/30 bg-tint-red/30 px-4 py-3.5 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-red-text [[data-skin=plate]_&]:bg-paper-raised"
      >
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <h3 id="identity-danger-title" className="text-[14.5px] font-semibold text-red-text [[data-skin=plate]_&]:font-black">
            {t.identities.dangerZone}
          </h3>
          <p className="text-[13px] text-ink-700">{t.identities.dangerHint}</p>
        </div>
        <Button
          variant="danger-outline"
          size="xs"
          className="shrink-0 [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-red-text"
          onClick={() => setConfirming(true)}
        >
          {t.identities.delete}
        </Button>
      </section>
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
