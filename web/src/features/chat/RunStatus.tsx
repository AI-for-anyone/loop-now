import { CircleAlertIcon, LoaderCircleIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { isInProgress } from './live-state'
import { runStatusText } from './run-status-text'

type Run = Schemas['Run']

// 回复的状态，放在回复上方名称与时间的那一行：进行中显示阶段文字；已打断、服务端重启中断与失败显示为结局印章，
// 已打断用粉色、服务端重启中断用藏青，都不用红色；正常完成不显示。状态都是静态的，回复中的动态由输出光标表示。
export function RunStatusBadge({ run }: { run: Run }) {
  if (run.status === 'failed') {
    return (
      <Badge variant="stamp" tone="danger" role="status">
        {t.chat.status.failed}
      </Badge>
    )
  }
  const text = runStatusText(run)
  if (!text) return null
  if (run.status === 'interrupted') {
    return (
      <Badge variant="stamp" tone={run.endReason === 'server_restart' || run.endReason === 'user_stopped' ? 'restart' : 'interrupted'} role="status">
        {text}
      </Badge>
    )
  }
  return (
    <span role="status" className={isInProgress(run) ? 'text-pink-text' : undefined}>
      {text}
    </span>
  )
}

// 失败的回复另加浅红说明块：失败原因的中文说明与服务端给出的错误信息。
export function FailureNote({ run }: { run: Run }) {
  if (run.status !== 'failed') return null
  return (
    <div role="status" className="flex items-start gap-2.5 self-start rounded-input bg-tint-red px-4 py-3 text-[14.5px] leading-[1.7]">
      <CircleAlertIcon aria-hidden="true" className="mt-1 size-4 shrink-0 text-red-text" />
      <span className="font-black [overflow-wrap:anywhere] text-red-text">{runStatusText(run)}</span>
    </div>
  )
}

// 工具进行中的旋转图标；减少动态效果时去掉旋转图标，换成静态的“…”。
function ToolSpinner() {
  return (
    <>
      <LoaderCircleIcon aria-hidden="true" data-slot="tool-spinner" className="riso-spin motion-reduce:hidden motion-reduce:animate-none" />
      <span aria-hidden="true" data-slot="tool-static-mark" className="hidden motion-reduce:inline">
        {t.chat.toolStaticMark}
      </span>
    </>
  )
}

// 工具调用只显示名称与状态，不显示参数与输出。Run.tools 是状态变化的记录，
// 同一个 toolCallId 可能有多项，按出现顺序合并，只显示最新的状态。
// 这一轮被打断时，没有结束的工具变灰、改为虚线边框，标明已打断。
export function ToolList({ tools: events, interrupted = false }: { tools: Schemas['ToolEvent'][]; interrupted?: boolean }) {
  const latest = new Map<string, Schemas['ToolEvent']>()
  for (const event of events) latest.set(event.toolCallId, event)
  const tools = [...latest.values()]
  if (tools.length === 0) return null
  return (
    <ul className="flex flex-wrap gap-2" aria-label={t.chat.toolsLabel}>
      {tools.map((tool) =>
        interrupted && tool.state === 'started' ? (
          <li key={tool.toolCallId}>
            <Badge data-interrupted className="border-[1.5px] border-dashed border-ink/30 bg-paper-raised text-ink-500">
              <span className="font-mono">{tool.toolName}</span>
              <span>{t.chat.toolInterrupted}</span>
            </Badge>
          </li>
        ) : (
          <li key={tool.toolCallId}>
            <Badge tone={tool.state === 'failed' ? 'danger' : tool.state === 'succeeded' ? 'success' : 'info'}>
              {tool.state === 'started' && <ToolSpinner />}
              <span className="font-mono text-ink">{tool.toolName}</span>
              <span>{t.chat.tool[tool.state]}</span>
            </Badge>
          </li>
        ),
      )}
    </ul>
  )
}
