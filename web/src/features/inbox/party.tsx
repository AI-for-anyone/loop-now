import { useSyncExternalStore } from 'react'
import { AgentAvatar } from '@/components/AgentAvatar'
import { inboxConnection } from '@/api/inbox-store'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import type { Participant } from './participants'

// Avatar: an Agent gets two overprinted circles (colors picked by ID); external identities and the console get a navy circle
// with a yellow @ and a pink shadow. Deleted ones are faded.
export function PartyAvatar({ party, className }: { party: Participant; className?: string }) {
  if (party.kind === 'agent' && party.id) {
    return <AgentAvatar id={party.id} className={cn(party.deleted && 'opacity-50', className)} />
  }
  return (
    <span
      aria-hidden="true"
      className={cn(
        'inline-flex size-9 shrink-0 items-center justify-center rounded-full bg-ink font-mono text-base font-bold text-riso-yellow shadow-[6px_2px_0_var(--riso-pink)]',
        party.deleted && 'opacity-50',
        className,
      )}
    >
      @
    </span>
  )
}

// Deleted mark: a small light yellow pill.
export function DeletedMark({ label, className }: { label: string; className?: string }) {
  return <span className={cn('shrink-0 rounded-full bg-tint-yellow px-2 text-[11.5px] leading-[20px] font-black text-ink', className)}>{label}</span>
}

// Connection state of the inbox event stream: when connected, show “实时更新中” (Live); while reconnecting after a drop, reuse
// the Chat tab's “连接中断，正在重连…” (Connection lost, reconnecting…); nothing is shown during the first connection.
export function InboxLiveStatus({ className }: { className?: string }) {
  const state = useSyncExternalStore(inboxConnection.subscribe, inboxConnection.get)
  if (state === 'connecting') return <span role="status" aria-live="polite" className={className} />
  const open = state === 'open'
  return (
    <span
      role="status"
      aria-live="polite"
      className={cn('inline-flex items-center gap-1.5 text-[12.5px] font-bold', open ? 'text-green-text' : 'text-red-text', className)}
    >
      <span aria-hidden="true" className={cn('size-2 rounded-full', open ? 'bg-green' : 'bg-red')} />
      {open ? t.inbox.live : t.chat.connection.reconnecting}
    </span>
  )
}
