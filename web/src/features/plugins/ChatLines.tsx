import { PlugIcon, TerminalIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
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
      if (!result.resumeSessions?.length) return text.noSessionsToResume
      return result.resumeSessions.map(session => `${session.current ? text.currentSession : ''}${session.id} · ${t.chat.time(session.updatedAt)}`).join('\n')
    default:
      return result.reply ?? text.noReply
  }
}

// 平台或插件认领的 / 命令：一行淡色说明与结果，不作为普通对话消息。
export function CommandLine({ command, result, at }: { command: string; result: Schemas['ConsoleCommandResult']; at: string }) {
  const platform = result.source === 'platform'
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
      <p className="ml-5 rounded-input bg-ink/[0.04] px-3 py-2 text-[13.5px] leading-[1.6] whitespace-pre-wrap text-ink-700 [overflow-wrap:anywhere]">{platform ? platformReply(result) : result.reply ?? text.noReply}</p>
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
