import { useRef, useState } from 'react'
import { PlugIcon, TerminalIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { Button } from '@/components/ui/button'
import { t } from '@/i18n'
import { copyText } from '@/lib/clipboard'
import { receiptClass, receiptPartClass } from '@/lib/receipt'
import { cn } from '@/lib/utils'

const text = t.plugins.chat

function platformReply(result: Schemas['ConsoleCommandResult']): string {
  const text = t.chat.commands
  switch (result.command) {
    case 'session':
      return result.session ? text.session(result.session) : text.noReply
    case 'compact': {
      // 结果在这次压缩的执行上（compactRun.compaction）：压缩了写前后的 token；Pi 没有压缩时按原因说明，这时 token 为 null。
      const compaction = result.compactRun?.compaction
      if (compaction?.outcome === 'skipped' && compaction.skipReason) return text.compactSkipped[compaction.skipReason]
      const before = compaction?.tokensBefore ?? result.tokensBefore
      const after = compaction?.estimatedTokensAfter ?? result.estimatedTokensAfter
      return before != null && after != null ? text.compact(before, after) : text.noReply
    }
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

// /resume 列出的会话，按列表的规则排：上面一道 2px 实线，行与行之间 1.5px 虚线；ID 与时间用等宽字；
// 当前会话标一个黄色标签（选中态一律是黄色），其余各行右边是“恢复”。
function ResumeSessionList({ sessions, onResume }: { sessions: Schemas['ConsoleResumeSession'][]; onResume?: (sessionId: string) => void }) {
  const text = t.chat.commands
  return (
    <div role="list" aria-label={text.resumeList} className="flex w-full max-w-[640px] flex-col border-t-2 border-ink">
      {sessions.map((session) => {
        const title = session.title?.trim() || text.noTextSession
        const shortID = session.id.length > 12 ? `${session.id.slice(0, 8)}…${session.id.slice(-4)}` : session.id
        return (
          <div role="listitem" key={session.id} className="flex min-w-0 items-center gap-3 border-b-[1.5px] border-dashed border-ink/25 px-1 py-2.5 text-ink-700">
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                <span className="text-[13.5px] font-black text-ink [overflow-wrap:anywhere]">{title}</span>
                {session.current && <span className="rounded-stamp bg-riso-yellow px-1.5 text-[11px] leading-[18px] font-black text-ink">{text.currentSession}</span>}
              </div>
              {session.preview && session.preview !== session.title && <p className="mt-0.5 line-clamp-2 text-xs leading-5 text-ink-500 [overflow-wrap:anywhere]">{session.preview}</p>}
              <div className="mt-1 flex flex-wrap items-center gap-x-1.5 text-[11.5px] text-ink-500">
                <code className="font-mono" title={session.id}>{shortID}</code>
                <span aria-hidden="true">{t.chat.metaSeparator}</span>
                <time dateTime={session.updatedAt} className="font-mono">{t.chat.time(session.updatedAt)}</time>
              </div>
            </div>
            {!session.current && onResume && (
              <Button type="button" size="xs" variant="secondary" className="border-2 border-ink bg-paper-raised hover:bg-white" onClick={() => onResume(session.id)}>
                {text.resumeAction}
              </Button>
            )}
          </div>
        )
      })}
    </div>
  )
}

// /copy 的内容没能自动写进剪贴板（Safari 里已不在按键手势内，或用 http 的局域网地址打开时没有剪贴板接口）：
// 内容放在只读的框里，旁边的“复制”是一次新的点击，这时浏览器会放行；仍然不行就选中框里的内容，直接按复制键。
function ManualCopy({ text: content }: { text: string }) {
  const box = useRef<HTMLTextAreaElement>(null)
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    if (await copyText(content)) {
      setCopied(true)
      return
    }
    box.current?.focus()
    box.current?.select()
  }
  return (
    <div className="ml-1 flex w-full max-w-[640px] flex-col gap-2 border-l-2 border-dashed border-ink/40 py-0.5 pl-3">
      <p className="text-[13.5px] leading-[1.6] text-ink-700">{t.chat.commands.copyManual}</p>
      <div className="flex min-w-0 items-start gap-2.5">
        <textarea
          ref={box}
          readOnly
          aria-label={t.chat.commands.copyManualLabel}
          value={content}
          rows={Math.min(6, content.split('\n').length + 1)}
          onFocus={(event) => event.currentTarget.select()}
          className="min-w-0 flex-1 resize-y rounded-ticket border-2 border-ink bg-paper-raised px-3 py-2 font-mono text-[12.5px] leading-[1.6] text-ink outline-none focus-visible:shadow-focus"
        />
        <Button type="button" size="xs" variant="secondary" className="border-2 border-ink bg-paper-raised hover:bg-white" onClick={() => void copy()}>
          {copied ? t.common.copied : t.common.copy}
        </Button>
      </div>
    </div>
  )
}

// 平台或插件认领的 / 命令：不作为普通对话消息。抬头是一张分段的小票（命令｜来源｜时间），结果排在下面，左侧一道虚线。
// copyFailed：/copy 的内容没能写进剪贴板，改成说明原因并给出手动复制的办法。
export function CommandLine({ command, result, at, onResume, copyFailed = false }: { command: string; result: Schemas['ConsoleCommandResult']; at: string; onResume?: (sessionId: string) => void; copyFailed?: boolean }) {
  const platform = result.source === 'platform'
  const manualCopy = platform && result.command === 'copy' && copyFailed && result.copyText != null ? result.copyText : null
  const showResumeSessions = platform && result.command === 'resume' && !result.resumedSession && Boolean(result.resumeSessions?.length)
  return (
    <div role="note" data-console-command className="flex min-w-0 flex-col items-start gap-2 text-xs leading-5 text-ink-500">
      <div className={receiptClass}>
        <span data-receipt-part className="inline-flex min-w-0 items-center gap-1.5 px-[11px]">
          <TerminalIcon aria-hidden="true" className="size-3.5 shrink-0" strokeWidth={2.2} />
          <code className="min-w-0 truncate font-mono font-bold">{command}</code>
        </span>
        <span data-receipt-part className={receiptPartClass}>
          {platform ? t.chat.commands.platform : text.command(result.plugin)}
        </span>
        <time data-receipt-part dateTime={at} className={cn(receiptPartClass, 'font-mono')}>
          {t.chat.time(at)}
        </time>
      </div>
      {showResumeSessions ? (
        <ResumeSessionList sessions={result.resumeSessions!} onResume={onResume} />
      ) : manualCopy !== null ? (
        <ManualCopy text={manualCopy} />
      ) : (
        <p className="ml-1 border-l-2 border-dashed border-ink/40 py-0.5 pl-3 text-[13.5px] leading-[1.6] whitespace-pre-wrap text-ink-700 [overflow-wrap:anywhere]">{platform ? platformReply(result) : result.reply ?? text.noReply}</p>
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
