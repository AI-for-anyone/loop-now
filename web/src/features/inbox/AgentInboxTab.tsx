import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { ErrorAlert } from '@/components/ErrorAlert'
import { RulerTicks, Stub, StubFigure } from '@/components/Sheet'
import { useAgentInbox } from '@/api/inbox'
import { useAgentContext } from '@/features/agents/agent-context'
import { t } from '@/i18n'
import { InboxLiveStatus } from './party'
import { ReadonlyMessage } from './ReadonlyMessage'

// Agent 详情的“收件箱”页签：只读查看这个 Agent 收到的 Inbox 消息，新的在前，可以只看未读，按页加载更多。直接排在页签的纸上。
// 已读状态随收件箱事件流实时变化；控制台不替 Agent 标记已读或未读。
export function AgentInboxTab() {
  const { agent } = useAgentContext()
  const [unreadOnly, setUnreadOnly] = useState(false)
  const inbox = useAgentInbox(agent.id, unreadOnly)
  const messages = inbox.data?.pages.flatMap((page) => page.messages) ?? []

  // 纸张里左边是信件列表（自己滚动），右边是票根里的计数（窄屏不显示）。
  return (
    <div className="flex h-full min-h-0">
      <div className="h-full min-w-0 flex-1 overflow-y-auto pt-6 pr-[clamp(14px,3vw,40px)] pb-14 pl-[clamp(12px,2vw,34px)]">
        <div className="flex max-w-[1040px] flex-col gap-3.5">
          {/* 顶上一行：筛选胶囊，右边是只读说明与实时更新；下面一道实线。 */}
          <div data-inbox-bar className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2.5 border-b-2 border-ink pb-3.5">
            <ToggleGroup aria-label={t.agentInbox.filter} className="gap-2 bg-transparent p-0" value={unreadOnly ? 'unread' : 'all'} onValueChange={(value) => setUnreadOnly(value === 'unread')}>
              <ToggleGroupItem className={pillClass} value="all">{t.agentInbox.all}</ToggleGroupItem>
              <ToggleGroupItem className={pillClass} value="unread">{t.agentInbox.unreadOnly}</ToggleGroupItem>
            </ToggleGroup>
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-[12px] text-ink-500">
              <span>{t.agentInbox.readonly}</span>
              <InboxLiveStatus className="text-[12px] font-black" />
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
        </div>
      </div>
      <MailStub agentId={agent.id} />
    </div>
  )
}

// 筛选胶囊：选中的一颗黄底、藏青描边加藏青投影，没选中的透明（改版通用规则第 7 条）。
const pillClass =
  'h-[34px] min-h-0 border-2 border-transparent px-4 data-[state=on]:mr-[3px] data-[state=on]:border-ink data-[state=on]:bg-riso-yellow data-[state=on]:px-[18px] data-[state=on]:shadow-[3px_3px_0_var(--ink)]'

// 票根里的计数：未读数与总数。按全部信件计（只看未读时也是），只算已经读到的几页，没读完时数字后面加“+”。
function MailStub({ agentId }: { agentId: string }) {
  const all = useAgentInbox(agentId, false)
  const messages = all.data?.pages.flatMap((page) => page.messages) ?? []
  const more = all.hasNextPage ? '+' : ''
  const unread = messages.filter((message) => message.readAt === null && message.cancelledAt === null).length
  return (
    <Stub as="aside" aria-label={t.agentInbox.stub.title} label={t.agentInbox.stub.title} className="w-[150px]">
      {all.isSuccess && (
        <div className="relative flex flex-1 flex-col gap-[22px] pt-1.5 pr-[30px] pl-[18px]">
          <RulerTicks />
          <StubFigure tone="pink" value={`${unread}${more}`} label={t.agentInbox.stub.unread} />
          <StubFigure value={`${messages.length}${more}`} label={t.agentInbox.stub.all} />
        </div>
      )}
    </Stub>
  )
}
