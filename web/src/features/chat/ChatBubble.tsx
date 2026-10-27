import type { ReactNode } from 'react'
import { AlarmClockIcon, MailIcon } from 'lucide-react'
import { sleepWakeNote } from '@/features/sleep/wake-text'
import { t } from '@/i18n'
import { avatarPair } from '@/lib/avatar'
import { cn } from '@/lib/utils'

// Presentation only: task adapters never invent a normal Message/Run or query
// the normal Agent's runtime to share these visual components.
// 打断发出的消息：浅粉底加荧光粉描边，名称一行带一个小小的“打断”。
// wakeSleepId：这条消息提前唤醒了 sleep，名称一行标“提前唤醒 sleep”。
export function UserChatBubble({ text, time, interrupted = false, wakeSleepId, images }: { images?: ReactNode; text: string; time: string; interrupted?: boolean; wakeSleepId?: string | null }) {
  return <div className="flex flex-col items-end gap-1.5">
    <span className="inline-flex items-center gap-2 text-[12.5px] font-bold text-ink-500"><span>{t.chat.you}</span> {t.chat.metaSeparator} {time}{interrupted && <span className="text-[11.5px] font-black text-pink-text">{t.chat.interruptTag}</span>}{wakeSleepId && <EarlyWake sleepId={wakeSleepId} />}</span>
    <div className={cn('max-w-[80%] rounded-[20px_20px_4px_20px] px-5 py-3.5 text-base leading-[1.8] font-medium whitespace-pre-wrap [overflow-wrap:anywhere]', interrupted ? 'border-[1.5px] border-riso-pink bg-tint-pink-2' : 'bg-tint-pink shadow-print-blue')}>{text}{images}</div>
  </div>
}
export function PlatformChatReminder({ text, at, wakeSleepId, label }: { text: string; at: string; wakeSleepId?: string | null; label?: ReactNode }) {
  return <div role="note" className="riso-tilt max-w-[600px] self-center rounded-[2px] px-5 py-3.5 [--tilt-bg:var(--riso-yellow)] [--tilt-shadow:3px_3px_0_rgb(24_38_94/.18)] [--tilt:-0.8deg] after:absolute after:-top-2 after:left-6 after:h-[18px] after:w-16 after:-rotate-[4deg] after:bg-white/60 after:content-['']">
    <span className="flex gap-2.5 text-[14.5px] leading-[1.75]"><MailIcon aria-hidden="true" className="mt-1 size-4 shrink-0" strokeWidth={2.2} /><span className="min-w-0 whitespace-pre-wrap [overflow-wrap:anywhere]"><span className="mr-3 font-black">{label ?? t.chat.platformReminderAt(at)}</span>{text}{wakeSleepId && <span className="mt-2 block"><EarlyWake sleepId={wakeSleepId} /></span>}</span></span>
  </div>
}
// sleep 到期唤醒：一行淡色元信息，不是消息。依次是闹钟、“sleep 到期唤醒”、时间、睡了多久（知道时）和 Agent 的说明；
// 说明截断成一行（手机宽度下另起一行，与文字对齐），完整说明与 sleep ID 在悬停提示里，平台写给模型的指令不显示。
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
// 被新消息提前唤醒的 sleep：一个小标记，sleep ID 在悬停提示里。
export function EarlyWake({ sleepId }: { sleepId: string }) {
  return <span data-early-wake title={t.sleep.idTip(sleepId)} className="inline-flex shrink-0 items-center gap-1 text-[11.5px] font-bold text-ink-500"><AlarmClockIcon aria-hidden="true" className="size-3" strokeWidth={2.4} />{t.sleep.wokeEarly}</span>
}
export function AgentChatReply({ agent, metadata, children }: { agent: { id: string; name: string }; metadata: ReactNode; children: ReactNode }) {
  const [back, front] = avatarPair(agent.id)
  return <article className="flex flex-col gap-2" aria-label={t.chat.agent}>
    <div className="flex flex-wrap items-center gap-2 text-[12.5px] font-bold text-ink-500"><span aria-hidden="true" className="relative inline-block h-[18px] w-6 shrink-0"><span className="absolute top-0 left-0 size-4 rounded-full" style={{ background: back }} /><span className="absolute top-0.5 left-2 size-4 rounded-full mix-blend-multiply" style={{ background: front }} /></span><span className="text-ink">{agent.name}</span><span aria-hidden="true">{t.chat.metaSeparator}</span>{metadata}</div>{children}
  </article>
}
