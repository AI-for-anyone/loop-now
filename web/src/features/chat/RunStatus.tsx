import { CircleAlertIcon, LoaderCircleIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { Badge } from '@/components/ui/badge'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { isInProgress } from './live-state'
import type { AgentRuntime } from '@/lib/runtime'
import { runStatusText, useRunRuntimeName } from './run-status-text'
import type { UnfinishedTool } from './tool-state'

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
// server. runtime: the Agent's runtime, named when the turn failed before it began.
export function FailureNote({ run, runtime }: { run: Run; runtime?: AgentRuntime }) {
  const runtimeName = useRunRuntimeName(runtime)
  if (run.status !== 'failed') return null
  return (
    <div role="status" className="flex items-start gap-2.5 self-start rounded-input bg-tint-red px-4 py-3 text-[14.5px] leading-[1.7]">
      <CircleAlertIcon aria-hidden="true" className="mt-1 size-4 shrink-0 text-red-text" />
      <span className="font-black [overflow-wrap:anywhere] text-red-text">{runStatusText(run, runtimeName(run))}</span>
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

// The state a tool shows: started and no_result follow the turn (see unfinishedTool); succeeded and failed are what the runtime reported.
function shownState(state: Schemas['ToolState'], unfinished: UnfinishedTool): Schemas['ToolState'] | 'interrupted' {
  if (state !== 'started' && state !== 'no_result') return state
  if (unfinished === 'interrupted') return 'interrupted'
  return state === 'no_result' || unfinished === 'no_result' ? 'no_result' : 'started'
}

// Tool calls show only their name and state, not arguments or output. Run.tools is a log of state changes,
// so the same toolCallId may have several entries; they are merged in order of appearance and only the latest state is shown.
// Interrupted tools turn gray with a dashed border; those with no result get a plain gray chip, told apart from running (blue, spinning),
// succeeded (green) and failed (red).
// exclude: tool calls shown another way (those that went on in the background); after: those, as list items of their own after the rest
// (given only when there are some).
export function ToolList({ tools: events, unfinished = 'running', exclude, after }: { tools: Schemas['ToolEvent'][]; unfinished?: UnfinishedTool; exclude?: ReadonlySet<string>; after?: ReactNode }) {
  const latest = new Map<string, Schemas['ToolEvent']>()
  for (const event of events) if (!exclude?.has(event.toolCallId)) latest.set(event.toolCallId, event)
  const tools = [...latest.values()]
  if (tools.length === 0 && !after) return null
  return (
    <ul className="flex flex-wrap gap-2" aria-label={t.chat.toolsLabel}>
      {tools.map((tool) => {
        const state = shownState(tool.state, unfinished)
        return (
          <li key={tool.toolCallId}>
            {state === 'interrupted' ? (
              <Badge data-interrupted className="border border-dashed border-hairline-strong bg-surface-control text-ink-500">
                <span className="font-mono">{tool.toolName}</span>
                <span>{t.chat.toolInterrupted}</span>
              </Badge>
            ) : state === 'no_result' ? (
              <Badge data-no-result className="border border-hairline bg-surface-subtle text-ink-700">
                <span className="font-mono text-ink">{tool.toolName}</span>
                <span>{t.chat.tool.no_result}</span>
              </Badge>
            ) : (
              <Badge tone={state === 'failed' ? 'danger' : state === 'succeeded' ? 'success' : 'info'}>
                {state === 'started' && <ToolSpinner />}
                <span className="font-mono text-ink">{tool.toolName}</span>
                <span>{t.chat.tool[state]}</span>
              </Badge>
            )}
          </li>
        )
      })}
      {after}
    </ul>
  )
}
