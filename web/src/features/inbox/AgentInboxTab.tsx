import { useState } from 'react'
import { EyeIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useAgentInbox } from '@/api/inbox'
import { useAgentContext } from '@/features/agents/agent-context'
import { t } from '@/i18n'
import { InboxLiveStatus } from './party'
import { ReadonlyMessage } from './ReadonlyMessage'

// Agent 详情的“收件箱”页签：只读查看这个 Agent 收到的 Inbox 消息，新的在前，可以只看未读，按页加载更多。
// 已读状态随收件箱事件流实时变化；控制台不替 Agent 标记已读或未读。
export function AgentInboxTab() {
  const { agent } = useAgentContext()
  const [unreadOnly, setUnreadOnly] = useState(false)
  const inbox = useAgentInbox(agent.id, unreadOnly)
  const messages = inbox.data?.pages.flatMap((page) => page.messages) ?? []

  return (
    <div className="h-full overflow-y-auto px-6 pt-5 pb-14">
      <div className="flex max-w-[860px] flex-col gap-4">
        <div className="flex flex-wrap items-center gap-x-[18px] gap-y-3">
          <ToggleGroup aria-label={t.agentInbox.filter} value={unreadOnly ? 'unread' : 'all'} onValueChange={(value) => setUnreadOnly(value === 'unread')}>
            <ToggleGroupItem value="all">{t.agentInbox.all}</ToggleGroupItem>
            <ToggleGroupItem value="unread">{t.agentInbox.unreadOnly}</ToggleGroupItem>
          </ToggleGroup>
          <span className="inline-flex items-center gap-1.5 rounded-full bg-tint-yellow px-3 py-0.5 text-[13px] font-bold">
            <EyeIcon aria-hidden="true" className="size-3.5" strokeWidth={2.4} />
            {t.agentInbox.readonly}
          </span>
          <InboxLiveStatus className="ml-auto" />
        </div>
        {inbox.isPending && <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
        {inbox.isError && <ErrorAlert error={inbox.error} />}
        {inbox.isSuccess && messages.length === 0 && (
          <p className="p-6 text-center text-[14px] text-ink-500">{unreadOnly ? t.agentInbox.emptyUnread : t.agentInbox.empty}</p>
        )}
        {messages.length > 0 && (
          <ul aria-label={t.agentInbox.list(agent.name)} className="flex flex-col gap-3">
            {messages.map((message) => (
              <ReadonlyMessage key={message.id} owner={{ kind: 'agent', agentId: agent.id }} message={message} />
            ))}
          </ul>
        )}
        {inbox.hasNextPage && (
          <Button variant="secondary" className="self-center" disabled={inbox.isFetchingNextPage} onClick={() => void inbox.fetchNextPage()}>
            {t.inbox.loadMore}
          </Button>
        )}
      </div>
    </div>
  )
}
