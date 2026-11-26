import type { ReactNode } from 'react'
import { AlarmClockIcon, MailIcon } from 'lucide-react'
import { AgentAvatar } from '@/components/AgentAvatar'
import { sleepWakeNote } from '@/features/sleep/wake-text'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// Presentation only: task adapters never invent a normal Message/Run or query
// the normal Agent's runtime to share these visual components.
// A message sent as an interrupt: light pink/amber fill with an accent border, and a small "Interrupt" tag on the name line.
// wakeSleepId: this message woke a sleep early; the name line is marked "Woke early".
// wide: preserved for Plate skin compatibility (on wide screens Plate widens to 88%); mono skin stays crisp at max-w-[82%] inside
// the centered 800px conversation column.
export function UserChatBubble({ text, time, interrupted = false, wakeSleepId, images, wide = false }: { images?: ReactNode; text: string; time: string; interrupted?: boolean; wakeSleepId?: string | null; wide?: boolean }) {
  return (
    <div className="flex flex-col items-end gap-1">
      <span className="inline-flex items-center gap-1.5 text-[11.5px] font-medium text-ink-500 [[data-skin=plate]_&]:gap-2 [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:font-bold">
        <span>{t.chat.you}</span> {t.chat.metaSeparator} {time}
        {interrupted && <span className="text-[11px] font-semibold text-pink-text [[data-skin=plate]_&]:text-[11.5px] [[data-skin=plate]_&]:font-black">{t.chat.interruptTag}</span>}
        {wakeSleepId && <EarlyWake sleepId={wakeSleepId} />}
      </span>
      <div
        className={cn(
          'max-w-[82%] rounded-2xl px-4 py-2.5 text-[14px] leading-[1.6] font-normal whitespace-pre-wrap [overflow-wrap:anywhere] [[data-skin=plate]_&]:max-w-[80%] [[data-skin=plate]_&]:rounded-[20px_20px_4px_20px] [[data-skin=plate]_&]:px-5 [[data-skin=plate]_&]:py-3.5 [[data-skin=plate]_&]:text-base [[data-skin=plate]_&]:leading-[1.8] [[data-skin=plate]_&]:font-medium',
          wide && '[[data-skin=plate]_&]:nav:max-w-[min(88%,1100px)]',
          interrupted
            ? 'border border-pink-text/40 bg-tint-pink-2 [[data-skin=plate]_&]:border-[1.5px] [[data-skin=plate]_&]:border-riso-pink'
            : 'border border-hairline bg-surface-hover [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-pink [[data-skin=plate]_&]:shadow-print-blue',
        )}
      >
        {text}
        {images}
      </div>
    </div>
  )
}

export function PlatformChatReminder({ text, at, wakeSleepId, label }: { text: string; at: string; wakeSleepId?: string | null; label?: ReactNode }) {
  return (
    <div role="note" className="riso-tilt max-w-[600px] self-center rounded-card px-5 py-3.5 [--tilt-bg:var(--surface-subtle)] [--tilt-shadow:0_0_0_1px_var(--hairline)] [[data-skin=plate]_&]:rounded-[2px] [[data-skin=plate]_&]:[--tilt-bg:var(--riso-yellow)] [[data-skin=plate]_&]:[--tilt-shadow:3px_3px_0_rgb(24_38_94/.18)] [[data-skin=plate]_&]:[--tilt:-0.8deg] [[data-skin=plate]_&]:after:absolute [[data-skin=plate]_&]:after:-top-2 [[data-skin=plate]_&]:after:left-6 [[data-skin=plate]_&]:after:h-[18px] [[data-skin=plate]_&]:after:w-16 [[data-skin=plate]_&]:after:-rotate-[4deg] [[data-skin=plate]_&]:after:bg-white/60 [[data-skin=plate]_&]:after:content-['']">
      <span className="flex gap-2.5 text-[14.5px] leading-[1.75]">
        <MailIcon aria-hidden="true" className="mt-1 size-4 shrink-0" strokeWidth={2.2} />
        <span className="min-w-0 whitespace-pre-wrap [overflow-wrap:anywhere]">
          <span className="mr-3 font-black">{label ?? t.chat.platformReminderAt(at)}</span>
          {text}
          {wakeSleepId && <span className="mt-2 block"><EarlyWake sleepId={wakeSleepId} /></span>}
        </span>
      </span>
    </div>
  )
}

// A sleep that woke on schedule: one line of faint meta info, not a message. In order: an alarm clock,
// "Woke up on schedule", the time, how long it slept (when known) and the Agent's note.
// The note is truncated to one line (at phone width it starts a new line, aligned with the text); the full note and the sleep ID are
// in the hover tooltip. The instruction the platform wrote for the model is not shown.
export function SleepWakeLine({ text, at, sleepId, slept }: { text: string; at: string; sleepId?: string | null; slept?: number }) {
  const parsed = sleepWakeNote(text)
  const note = parsed?.note
  const tip = [parsed ? note : text, sleepId && t.sleep.idTip(sleepId)].filter(Boolean).join('\n')
  const dot = <span aria-hidden="true" className="shrink-0">{t.chat.metaSeparator}</span>
  return (
    <div role="note" data-sleep-wake title={tip || undefined} className="flex min-w-0 items-center gap-x-1.5 text-xs leading-5 text-ink-500 max-sm:flex-wrap">
      <AlarmClockIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.2} />
      <span className="shrink-0 font-bold">{t.sleep.woke}</span>
      {dot}
      <time dateTime={at} className="shrink-0">{t.chat.time(at)}</time>
      {slept !== undefined && <>{dot}<span className="shrink-0">{t.sleep.slept(slept)}</span></>}
      {note && <><span aria-hidden="true" className="shrink-0 max-sm:hidden">{t.chat.metaSeparator}</span><span className="min-w-0 truncate max-sm:basis-full max-sm:pl-5">{note}</span></>}
    </div>
  )
}

// A sleep woken early by a new message: a small marker, with the sleep ID in the hover tooltip.
export function EarlyWake({ sleepId }: { sleepId: string }) {
  return (
    <span data-early-wake title={t.sleep.idTip(sleepId)} className="inline-flex shrink-0 items-center gap-1 text-[11.5px] font-bold text-ink-500">
      <AlarmClockIcon aria-hidden="true" className="size-3" strokeWidth={2.4} />
      {t.sleep.wokeEarly}
    </span>
  )
}

export function AgentChatReply({
  agent,
  metadata,
  turnNumber,
  showTurnNumber = false,
  children,
}: {
  agent: { id: string; name: string }
  metadata: ReactNode
  turnNumber?: number | null
  showTurnNumber?: boolean
  children: ReactNode
}) {
  return (
    <article className="flex min-w-0 flex-col gap-2" aria-label={t.chat.agent}>
      <div className="flex flex-wrap items-center gap-2 text-[12px] font-medium text-ink-500 [[data-skin=plate]_&]:text-[12.5px] [[data-skin=plate]_&]:font-bold">
        <AgentAvatar id={agent.id} name={agent.name} size="sm" />
        <span className="text-[13px] font-semibold text-ink [[data-skin=plate]_&]:text-[13.5px] [[data-skin=plate]_&]:font-black">{agent.name}</span>
        <span aria-hidden="true">{t.chat.metaSeparator}</span>
        {metadata}
        {showTurnNumber && (
          <span
            aria-hidden="true"
            data-turn-number
            className="ml-auto font-mono text-[11px] font-medium text-ink-500 tabular-nums before:content-['#'] empty:hidden"
          >
            {turnNumber ?? ''}
          </span>
        )}
      </div>
      <div className="flex min-w-0 max-w-none flex-col gap-2.5">{children}</div>
    </article>
  )
}
