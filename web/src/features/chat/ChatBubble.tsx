import type { ReactNode } from 'react'
import { AlarmClockIcon, MailIcon } from 'lucide-react'
import { sleepWakeNote } from '@/features/sleep/wake-text'
import { t } from '@/i18n'
import { avatarPair } from '@/lib/avatar'
import { cn } from '@/lib/utils'

// Presentation only: task adapters never invent a normal Message/Run or query
// the normal Agent's runtime to share these visual components.
// A message sent as an interrupt: light pink fill with a fluorescent pink border, and a small “打断” (Interrupt) on the name line.
// wakeSleepId: this message woke a sleep early; the name line is marked “提前唤醒 sleep” (Woke early).
// wide: on a wide screen of the Agent chat page the bubble is at most 88% of the conversation column (no wider than the reply
// body's 1100px); narrow screens and task node conversations stay at 80%.
export function UserChatBubble({ text, time, interrupted = false, wakeSleepId, images, wide = false }: { images?: ReactNode; text: string; time: string; interrupted?: boolean; wakeSleepId?: string | null; wide?: boolean }) {
  return <div className="flex flex-col items-end gap-1.5">
    <span className="inline-flex items-center gap-2 text-[12.5px] font-bold text-ink-500"><span>{t.chat.you}</span> {t.chat.metaSeparator} {time}{interrupted && <span className="text-[11.5px] font-black text-pink-text">{t.chat.interruptTag}</span>}{wakeSleepId && <EarlyWake sleepId={wakeSleepId} />}</span>
    <div className={cn('max-w-[80%]', wide && 'nav:max-w-[min(88%,1100px)]', 'rounded-[20px_20px_4px_20px] px-5 py-3.5 text-base leading-[1.8] font-medium whitespace-pre-wrap [overflow-wrap:anywhere]', interrupted ? 'border-[1.5px] border-riso-pink bg-tint-pink-2' : 'bg-tint-pink shadow-print-blue')}>{text}{images}</div>
  </div>
}
export function PlatformChatReminder({ text, at, wakeSleepId, label }: { text: string; at: string; wakeSleepId?: string | null; label?: ReactNode }) {
  return <div role="note" className="riso-tilt max-w-[600px] self-center rounded-[2px] px-5 py-3.5 [--tilt-bg:var(--riso-yellow)] [--tilt-shadow:3px_3px_0_rgb(24_38_94/.18)] [--tilt:-0.8deg] after:absolute after:-top-2 after:left-6 after:h-[18px] after:w-16 after:-rotate-[4deg] after:bg-white/60 after:content-['']">
    <span className="flex gap-2.5 text-[14.5px] leading-[1.75]"><MailIcon aria-hidden="true" className="mt-1 size-4 shrink-0" strokeWidth={2.2} /><span className="min-w-0 whitespace-pre-wrap [overflow-wrap:anywhere]"><span className="mr-3 font-black">{label ?? t.chat.platformReminderAt(at)}</span>{text}{wakeSleepId && <span className="mt-2 block"><EarlyWake sleepId={wakeSleepId} /></span>}</span></span>
  </div>
}
// A sleep that woke on schedule: one line of faint meta info, not a message. In order: an alarm clock,
// “sleep 到期唤醒” (Woke up on schedule), the time, how long it slept (when known) and the Agent's note.
// The note is truncated to one line (at phone width it starts a new line, aligned with the text); the full note and the sleep ID are
// in the hover tooltip. The instruction the platform wrote for the model is not shown.
export function SleepWakeLine({ text, at, sleepId, slept }: { text: string; at: string; sleepId?: string | null; slept?: number }) {
  const parsed = sleepWakeNote(text), note = parsed?.note
  const tip = [parsed ? note : text, sleepId && t.sleep.idTip(sleepId)].filter(Boolean).join('\n')
  const dot = <span aria-hidden="true" className="shrink-0">{t.chat.metaSeparator}</span>
  return <div role="note" data-sleep-wake title={tip || undefined} className="flex min-w-0 items-center gap-x-1.5 text-xs leading-5 text-ink-500 max-sm:flex-wrap">
    <AlarmClockIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.2} />
    <span className="shrink-0 font-bold">{t.sleep.woke}</span>
    {dot}<time dateTime={at} className="shrink-0">{t.chat.time(at)}</time>
    {slept !== undefined && <>{dot}<span className="shrink-0">{t.sleep.slept(slept)}</span></>}
    {note && <><span aria-hidden="true" className="shrink-0 max-sm:hidden">{t.chat.metaSeparator}</span><span className="min-w-0 truncate max-sm:basis-full max-sm:pl-5">{note}</span></>}
  </div>
}
// A sleep woken early by a new message: a small marker, with the sleep ID in the hover tooltip.
export function EarlyWake({ sleepId }: { sleepId: string }) {
  return <span data-early-wake title={t.sleep.idTip(sleepId)} className="inline-flex shrink-0 items-center gap-1 text-[11.5px] font-bold text-ink-500"><AlarmClockIcon aria-hidden="true" className="size-3" strokeWidth={2.4} />{t.sleep.wokeEarly}</span>
}
export function AgentChatReply({ agent, metadata, children }: { agent: { id: string; name: string }; metadata: ReactNode; children: ReactNode }) {
  const [back, front] = avatarPair(agent.id)
  return <article className="flex flex-col gap-2.5" aria-label={t.chat.agent}>
    <div className="flex flex-wrap items-center gap-2 text-[12.5px] font-bold text-ink-500"><span aria-hidden="true" className="relative inline-block h-[18px] w-[26px] shrink-0"><span className="absolute top-0 left-0 size-[17px] rounded-full" style={{ background: back }} /><span className="absolute top-px left-[9px] size-[17px] rounded-full mix-blend-multiply" style={{ background: front }} /></span><span className="text-[13.5px] font-black text-ink">{agent.name}</span><span aria-hidden="true">{t.chat.metaSeparator}</span>{metadata}</div>{children}
  </article>
}
