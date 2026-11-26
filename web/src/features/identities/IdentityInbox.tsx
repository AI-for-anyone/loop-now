import { useId, useState } from 'react'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { ErrorAlert } from '@/components/ErrorAlert'
import { useIdentityInbox } from '@/api/inbox'
import type { InboxDirection } from '@/api/queries'
import { InboxLiveStatus } from '@/features/inbox/party'
import { ReadonlyMessage } from '@/features/inbox/ReadonlyMessage'
import { t } from '@/i18n'

const directions: InboxDirection[] = ['all', 'received', 'sent']

// An external identity's inbox (read-only): received and sent messages, filterable by direction and unread only; for sent
// messages, “未读” (Unread) means the other party hasn't read it yet.
// Viewing the full text doesn't change the read state, and there is no action to mark messages read on its behalf; deleted
// identities can be viewed too.
export function IdentityInbox({ identityId }: { identityId: string }) {
  const [direction, setDirection] = useState<InboxDirection>('all')
  const [unreadOnly, setUnreadOnly] = useState(false)
  const inbox = useIdentityInbox(identityId, direction, unreadOnly)
  const messages = inbox.data?.pages.flatMap((page) => page.messages) ?? []
  const unreadId = useId()

  return (
    <section aria-labelledby="identity-inbox" className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5">
        <h3 id="identity-inbox" className="text-[15px] leading-[1.3] font-semibold [[data-skin=plate]_&]:text-[17px] [[data-skin=plate]_&]:font-black">
          {t.identities.inbox}
        </h3>
        <span className="rounded-full border border-hairline bg-surface-subtle px-2.5 py-0.5 text-[12px] font-medium text-ink-700 [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-yellow [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-ink">{t.identities.inboxReadonly}</span>
        <InboxLiveStatus />
        <div className="ml-auto flex flex-wrap items-center gap-x-3.5 gap-y-2.5">
          <ToggleGroup aria-label={t.identities.direction} className={segmentedClass} value={direction} onValueChange={(value) => setDirection(value as InboxDirection)}>
            {directions.map((value) => (
              <ToggleGroupItem key={value} value={value} className={segmentedItemClass}>
                {t.identities.directions[value]}
              </ToggleGroupItem>
            ))}
          </ToggleGroup>
          <label htmlFor={unreadId} className="inline-flex min-h-8 cursor-pointer items-center gap-2 text-[13px] font-medium [[data-skin=plate]_&]:font-black">
            <input
              id={unreadId}
              type="checkbox"
              checked={unreadOnly}
              onChange={(event) => setUnreadOnly(event.target.checked)}
              className="m-0 size-4 accent-ink focus-visible:shadow-focus [[data-skin=plate]_&]:size-[18px] [[data-skin=plate]_&]:accent-riso-pink"
            />
            {t.identities.unreadOnly}
          </label>
        </div>
      </div>
      {inbox.isPending && <p className="text-[14px] text-ink-500">{t.common.loading}</p>}
      {inbox.isError && <ErrorAlert error={inbox.error} />}
      {inbox.isSuccess && messages.length === 0 && <p className="p-5 text-center text-[14px] text-ink-500">{t.identities.inboxEmpty}</p>}
      {messages.length > 0 && (
        <ul aria-label={t.identities.inbox} className="flex flex-col">
          {messages.map((message) => (
            <ReadonlyMessage key={message.id} layout="row" owner={{ kind: 'identity', identityId }} message={message} />
          ))}
        </ul>
      )}
      {inbox.hasNextPage && (
        <Button variant="secondary" className="self-center" disabled={inbox.isFetchingNextPage} onClick={() => void inbox.fetchNextPage()}>
          {t.inbox.loadMore}
        </Button>
      )}
    </section>
  )
}
