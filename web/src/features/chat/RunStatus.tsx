import { CircleAlertIcon, LoaderCircleIcon } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { isInProgress } from './live-state'
import { runStatusText } from './run-status-text'

type Run = Schemas['Run']

// A reply's status, on the name-and-time line above the reply: in progress shows the phase text; interrupted, cut off by a server
// restart, and failed show as outcome stamps,
// interrupted in pink and cut off by a server restart in navy, neither in red; a normal completion shows nothing. Statuses are all
// static; activity during a reply is conveyed by the output cursor.
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

// A failed reply also gets a light red note block: the localized explanation of the failure reason and the error message from the
// server.
export function FailureNote({ run }: { run: Run }) {
  if (run.status !== 'failed') return null
  return (
    <div role="status" className="flex items-start gap-2.5 self-start rounded-input bg-tint-red px-4 py-3 text-[14.5px] leading-[1.7]">
      <CircleAlertIcon aria-hidden="true" className="mt-1 size-4 shrink-0 text-red-text" />
      <span className="font-black [overflow-wrap:anywhere] text-red-text">{runStatusText(run)}</span>
    </div>
  )
}

// Spinning icon while a tool is running; with reduced motion the spinner is removed and replaced by a static "…".
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

// Tool calls show only their name and state, not arguments or output. Run.tools is a log of state changes,
// so the same toolCallId may have several entries; they are merged in order of appearance and only the latest state is shown.
// When this turn was interrupted, tools that hadn't finished turn gray with a dashed border, marked as interrupted.
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
