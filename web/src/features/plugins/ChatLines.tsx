import { PlugIcon, TerminalIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

const text = t.plugins.chat

// 对话框里的 / 命令被插件认领：一行淡色说明（命令、哪个插件、时间），下面是插件的回复。不是发出的消息。
export function CommandLine({ command, result, at }: { command: string; result: Schemas['ConsoleCommandResult']; at: string }) {
  return (
    <div role="note" data-console-command className="flex min-w-0 flex-col gap-1 text-xs leading-5 text-ink-500">
      <div className="flex min-w-0 flex-wrap items-center gap-x-1.5">
        <TerminalIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.2} />
        <code className="min-w-0 truncate font-mono font-bold text-ink-700">{command}</code>
        <span aria-hidden="true">{t.chat.metaSeparator}</span>
        <span>{text.command(result.plugin)}</span>
        <span aria-hidden="true">{t.chat.metaSeparator}</span>
        <time dateTime={at}>{t.chat.time(at)}</time>
      </div>
      <p className="ml-5 rounded-input bg-ink/[0.04] px-3 py-2 text-[13.5px] leading-[1.6] whitespace-pre-wrap text-ink-700 [overflow-wrap:anywhere]">{result.reply ?? text.noReply}</p>
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
