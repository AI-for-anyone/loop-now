import { useState } from 'react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { ErrorAlert } from '@/components/ErrorAlert'
import { PageBody } from '@/components/Sheet'
import { useAgentInbox } from '@/api/inbox'
import { useAgentContext } from '@/features/agents/agent-context'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { InboxLiveStatus } from './party'
import { ReadonlyMessage } from './ReadonlyMessage'

// The Inbox tab of Agent details: a read-only view of the Inbox messages this Agent received, newest first, with counts embedded
// directly inside the filter pills so they remain visible at every viewport width.
// Read state changes live with the inbox event stream; the console never marks messages read or unread on the Agent's behalf.
export function AgentInboxTab() {
  const { agent } = useAgentContext()
  const [unreadOnly, setUnreadOnly] = useState(false)
  const all = useAgentInbox(agent.id, false)
  const inbox = useAgentInbox(agent.id, unreadOnly)
  const messages = inbox.data?.pages.flatMap((page) => page.messages) ?? []
  const allMessages = all.data?.pages.flatMap((page) => page.messages) ?? []
  const more = all.hasNextPage ? '+' : ''
  const unreadCount = allMessages.filter((message) => message.readAt === null && message.cancelledAt === null).length

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <PageBody width="wide" innerClassName="flex flex-col gap-3.5">
        {/* Top bar: filter pills with embedded counts, compact Read-only badge with tooltip, and live status. */}
        <div data-inbox-bar className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2.5 border-b border-hairline pb-3 [[data-skin=plate]_&]:border-b-2 [[data-skin=plate]_&]:border-ink">
          <ToggleGroup aria-label={t.agentInbox.filter} className="gap-2 bg-transparent p-0" value={unreadOnly ? 'unread' : 'all'} onValueChange={(value) => setUnreadOnly(value === 'unread')}>
            <ToggleGroupItem className={pillClass} value="all">
              <span>{t.agentInbox.all}</span>
              {all.isSuccess && (
                <span data-count="all" className="ml-1.5 font-mono text-[11.5px] font-medium text-ink-500 tabular-nums [[data-skin=plate]_&]:font-extrabold [[data-skin=plate]_&]:text-ink">
                  {`${allMessages.length}${more}`}
                </span>
              )}
            </ToggleGroupItem>
            <ToggleGroupItem className={pillClass} value="unread">
              <span>{t.agentInbox.unreadOnly}</span>
              {all.isSuccess && (
                <span
                  data-count="unread"
                  className={cn(
                    'ml-1.5 font-mono text-[11.5px] font-medium tabular-nums [[data-skin=plate]_&]:font-extrabold',
                    unreadCount > 0 ? 'text-pink-text' : 'text-ink-500 [[data-skin=plate]_&]:text-ink',
                  )}
                >
                  {`${unreadCount}${more}`}
                </span>
              )}
            </ToggleGroupItem>
          </ToggleGroup>
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[12px] text-ink-500">
            <Badge variant="chip" tone="neutral" title={t.agentInbox.readonly} aria-label={t.agentInbox.readonly}>
              {t.agentInbox.readonlyBadge}
            </Badge>
            <InboxLiveStatus className="text-[12px] font-semibold [[data-skin=plate]_&]:font-black" />
          </div>
        </div>
        {inbox.isPending && <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
        {inbox.isError && <ErrorAlert error={inbox.error} />}
        {inbox.isSuccess && messages.length === 0 && (
          <p className="p-6 text-center text-[14px] text-ink-500">{unreadOnly ? t.agentInbox.emptyUnread : t.agentInbox.empty}</p>
        )}
        {messages.length > 0 && (
          <ul aria-label={t.agentInbox.list(agent.name)} className="flex flex-col">
            {messages.map((message) => (
              <ReadonlyMessage key={message.id} layout="row" owner={{ kind: 'agent', agentId: agent.id }} message={message} />
            ))}
          </ul>
        )}
        {inbox.hasNextPage && (
          <Button variant="secondary" className="self-center" disabled={inbox.isFetchingNextPage} onClick={() => void inbox.fetchNextPage()}>
            {t.inbox.loadMore}
          </Button>
        )}
      </PageBody>
    </div>
  )
}

// Filter pills: crisp hairline active state in mono skin; yellow with a navy outline and navy shadow in Plate skin.
const pillClass =
  'h-[34px] min-h-0 border border-transparent px-3.5 font-medium text-ink-700 hover:bg-surface-hover data-[state=on]:border-hairline-strong data-[state=on]:bg-surface-active data-[state=on]:font-semibold data-[state=on]:text-ink [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:px-4 [[data-skin=plate]_&]:data-[state=on]:mr-[3px] [[data-skin=plate]_&]:data-[state=on]:border-ink [[data-skin=plate]_&]:data-[state=on]:bg-riso-yellow [[data-skin=plate]_&]:data-[state=on]:px-[18px] [[data-skin=plate]_&]:data-[state=on]:shadow-[3px_3px_0_var(--ink)]'
