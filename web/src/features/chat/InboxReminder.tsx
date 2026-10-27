import type { Schemas } from '@/api/client'
import { PluginSenderChip } from '@/features/plugins/ChatLines'
import { useAgentInboxMessage } from '@/api/inbox'
import { partyDisplay } from '@/features/inbox/participants'
import { t } from '@/i18n'
import { EarlyWake } from './ChatBubble'

function shortPreview(value: string) {
  const chars = Array.from(value.replace(/\s+/gu, ' ').trim())
  return chars.length > 100 ? `${chars.slice(0, 100).join('')}…` : chars.join('')
}

// Only a summary of the original Inbox message, never the runtime's reminder
// instructions. Task messages and sleep reminders use their existing views.
// wakeSleepId: this Inbox message woke a sleep early; a small tag marks it.
export function InboxReminderLine({ at, sender, preview, wakeSleepId }: { at: string; sender?: Schemas['InboxParticipant']; preview?: string; wakeSleepId?: string | null }) {
  const source = sender ? partyDisplay(sender) : null
  const text = preview ? shortPreview(preview) : ''
  return <div role="note" data-inbox-reminder className="flex min-w-0 items-center gap-2 text-xs leading-5 text-ink-500">
    <span className="shrink-0 font-bold">{t.chat.inboxReminder.title}</span>
    <time dateTime={at} className="shrink-0">{t.chat.time(at)}</time>
    {wakeSleepId && <EarlyWake sleepId={wakeSleepId} />}
    {sender?.kind === 'plugin' ? <PluginSenderChip participant={sender} className="max-w-[200px] shrink" /> : source && <span className="max-w-[180px] shrink truncate">{t.chat.inboxReminder.from(source.title)}{source.deleted && t.chat.inboxReminder.deleted(source.deleted)}</span>}
    <span className="min-w-0 flex-1 truncate">{text || t.chat.inboxReminder.received}</span>
  </div>
}

export function InboxReminder({ agentId, messageId, at, wakeSleepId }: { agentId: string; messageId?: string; at: string; wakeSleepId?: string | null }) {
  const original = useAgentInboxMessage(agentId, messageId ?? '', Boolean(messageId))
  const message = original.data && original.data.id === messageId && original.data.to.kind === 'agent' && original.data.to.id === agentId ? original.data : undefined
  return <InboxReminderLine at={at} sender={message?.from} preview={message?.preview} wakeSleepId={wakeSleepId} />
}
