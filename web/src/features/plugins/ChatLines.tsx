import { PlugIcon, TerminalIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

const text = t.plugins.chat

function platformReply(result: Schemas['ConsoleCommandResult']): string {
  const text = t.chat.commands
  switch (result.command) {
    case 'session':
      return result.session ? text.session(result.session) : text.noReply
    case 'compact':
      return result.tokensBefore != null && result.estimatedTokensAfter != null ? text.compact(result.tokensBefore, result.estimatedTokensAfter) : text.noReply
    case 'reload':
      return result.processStopped ? text.reloaded : text.reloadNotNeeded
    case 'new':
      return result.resetSession ? text.newSession(result.resetSession.epoch) : text.noReply
    case 'copy':
      return result.copyText == null ? text.nothingToCopy : text.copied
    case 'export':
      return text.exported(result.exportFormat ?? 'html')
    case 'resume':
      if (result.resumedSession) return text.resumedSession(result.resumedSession.epoch)
      return text.noSessionsToResume
    default:
      return result.reply ?? text.noReply
  }
}

function ResumeSessionList({ sessions, onResume }: { sessions: Schemas['ConsoleResumeSession'][]; onResume?: (sessionId: string) => void }) {
  const text = t.chat.commands
  return (
    <div role="list" aria-label={text.resumeList} className="ml-5 flex flex-col gap-2">
      {sessions.map((session) => {
        const title = session.title?.trim() || text.noTextSession
        const shortID = session.id.length > 12 ? `${session.id.slice(0, 8)}…${session.id.slice(-4)}` : session.id
        return (
          <div role="listitem" key={session.id} className="flex min-w-0 items-start gap-3 rounded-input bg-ink/[0.04] px-3 py-2.5 text-ink-700">
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span className="text-[13.5px] font-black [overflow-wrap:anywhere]">{title}</span>
                {session.current && <span className="rounded-stamp bg-tint-blue px-1.5 py-px text-[11px] font-bold text-ink-500">{text.currentSession}</span>}
              </div>
              {session.preview && session.preview !== session.title && <p className="mt-0.5 line-clamp-2 text-xs leading-5 text-ink-500 [overflow-wrap:anywhere]">{session.preview}</p>}
              <div className="mt-1 flex flex-wrap items-center gap-x-1.5 text-[11.5px] text-ink-500">
                <code className="font-mono" title={session.id}>{shortID}</code>
                <span aria-hidden="true">{t.chat.metaSeparator}</span>
                <time dateTime={session.updatedAt}>{t.chat.time(session.updatedAt)}</time>
              </div>
            </div>
            {!session.current && onResume && <Button type="button" size="xs" variant="secondary" onClick={() => onResume(session.id)}>{text.resumeAction}</Button>}
          </div>
        )
      })}
    </div>
  )
}

// 平台或插件认领的 / 命令：一行淡色说明与结果，不作为普通对话消息。
export function CommandLine({ command, result, at, onResume }: { command: string; result: Schemas['ConsoleCommandResult']; at: string; onResume?: (sessionId: string) => void }) {
  const platform = result.source === 'platform'
  const showResumeSessions = platform && result.command === 'resume' && !result.resumedSession && Boolean(result.resumeSessions?.length)
  return (
    <div role="note" data-console-command className="flex min-w-0 flex-col gap-1 text-xs leading-5 text-ink-500">
      <div className="flex min-w-0 flex-wrap items-center gap-x-1.5">
        <TerminalIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.2} />
        <code className="min-w-0 truncate font-mono font-bold text-ink-700">{command}</code>
        <span aria-hidden="true">{t.chat.metaSeparator}</span>
        <span>{platform ? t.chat.commands.platform : text.command(result.plugin)}</span>
        <span aria-hidden="true">{t.chat.metaSeparator}</span>
        <time dateTime={at}>{t.chat.time(at)}</time>
      </div>
      {showResumeSessions ? (
        <ResumeSessionList sessions={result.resumeSessions!} onResume={onResume} />
      ) : (
        <p className="ml-5 rounded-input bg-ink/[0.04] px-3 py-2 text-[13.5px] leading-[1.6] whitespace-pre-wrap text-ink-700 [overflow-wrap:anywhere]">{platform ? platformReply(result) : result.reply ?? text.noReply}</p>
      )}
    </div>
  )
}

// 插件渠道来的发件人：一个小标签“插件 · 名称”，不链接到外部身份；插件已卸载时标出。
export function PluginSenderChip({ participant, className }: { participant: Schemas['InboxParticipant']; className?: string }) {
  return (
    <span title={participant.address ?? undefined} className={cn('inline-flex min-w-0 items-center gap-1 rounded-[6px] bg-tint-blue px-1.5 py-px text-[11.5px] leading-4 font-black text-ink', className)}>
      <PlugIcon aria-hidden="true" className="size-3 shrink-0" strokeWidth={2.4} />
      <span className="min-w-0 truncate">{text.sender(participant.name)}</span>
      {participant.deleted && <span className="shrink-0 font-bold text-ink-500">{text.uninstalled}</span>}
    </span>
  )
}
