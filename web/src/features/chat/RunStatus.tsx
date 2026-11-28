import { CircleAlertIcon, LoaderCircleIcon } from 'lucide-react'
import type { ReactNode } from 'react'
import { Badge } from '@/components/ui/badge'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { isInProgress } from './live-state'
import type { AgentRuntime } from '@/lib/runtime'
import { cn } from '@/lib/utils'
import { useSkin } from '@/theme/store'
import { runStatusText, useRunRuntimeName } from './run-status-text'
import { ToolListLeadingContext } from './tool-list-context'
import { groupTools, shownState, type ShownToolState, type UnfinishedTool } from './tool-state'

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
      <LoaderCircleIcon aria-hidden="true" data-slot="tool-spinner" className="size-3 shrink-0 riso-spin motion-reduce:hidden motion-reduce:animate-none" />
      <span aria-hidden="true" data-slot="tool-static-mark" className="hidden motion-reduce:inline">
        {t.chat.toolStaticMark}
      </span>
    </>
  )
}

function stateText(state: ShownToolState): string {
  return state === 'interrupted' ? t.chat.toolInterrupted : t.chat.tool[state]
}

// Tool calls show only their name and state, not arguments or output. Run.tools is a log of state changes,
// so the same toolCallId may have several entries; they are merged in order of appearance and only the latest state is shown.
// In the default mono skin, calls with the same name and state are grouped onto one quiet muted line (`execute ×9 · read · edit`),
// with failed groups in red, running with a spinner, and no-result/interrupted in gray; the Plate skin keeps individual pills.
// exclude: tool calls shown another way (those that went on in the background); after: those, as list items of their own after the rest
// (given only when there are some).
export function ToolList({ tools: events, unfinished = 'running', exclude, after }: { tools: Schemas['ToolEvent'][]; unfinished?: UnfinishedTool; exclude?: ReadonlySet<string>; after?: ReactNode }) {
  const skin = useSkin()
  const latest = new Map<string, Schemas['ToolEvent']>()
  for (const event of events) if (!exclude?.has(event.toolCallId)) latest.set(event.toolCallId, event)
  const tools = [...latest.values()]
  if (tools.length === 0 && !after) return null

  if (skin === 'plate') {
    return (
      <ul className="flex flex-wrap gap-2" aria-label={t.chat.toolsLabel}>
        {tools.map((tool) => {
          const state = shownState(tool.state, unfinished)
          return (
            <li key={tool.toolCallId}>
              {state === 'interrupted' ? (
                <Badge data-interrupted className="border border-dashed border-hairline-strong bg-surface-control text-ink-500">
                  <span data-tool-text="" className="inline-flex items-baseline gap-1">
                    <span className="font-mono">{tool.toolName}</span>
                    <span>{t.chat.toolInterrupted}</span>
                  </span>
                </Badge>
              ) : state === 'no_result' ? (
                <Badge data-no-result className="border border-hairline bg-surface-subtle text-ink-700">
                  <span data-tool-text="" className="inline-flex items-baseline gap-1">
                    <span className="font-mono text-ink">{tool.toolName}</span>
                    <span>{t.chat.tool.no_result}</span>
                  </span>
                </Badge>
              ) : (
                <Badge tone={state === 'failed' ? 'danger' : state === 'succeeded' ? 'success' : 'info'}>
                  {state === 'started' && <ToolSpinner />}
                  <span data-tool-text="" className="inline-flex items-baseline gap-1">
                    <span className="font-mono text-ink">{tool.toolName}</span>
                    <span>{t.chat.tool[state]}</span>
                  </span>
                </Badge>
              )}
            </li>
          )
        })}
        {after}
      </ul>
    )
  }

  const groups = groupTools(events, unfinished, exclude)
  return (
    <ToolListLeadingContext.Provider value={groups.length > 0}>
      <ul
        className="mt-1.5 -mb-1 flex flex-wrap items-center gap-y-1.5 text-[12.5px] leading-5 text-ink-500 [&>li:not([data-tool-group])]:ml-2 [&>li:first-child]:ml-0"
        aria-label={t.chat.toolsLabel}
      >
        {groups.map((group, index) => {
          const label = stateText(group.state)
          return (
            <li key={group.key} data-tool-group="" data-state={group.state} className="inline-flex items-center">
              {index > 0 && (
                <span aria-hidden="true" className="mx-1.5 text-ink-500/45 select-none">
                  {t.chat.metaSeparator}
                </span>
              )}
              <span className="sr-only">{t.chat.toolGroupLabel(group.toolName, label, group.count)}</span>
              <span
                aria-hidden="true"
                data-tool-visual=""
                data-interrupted={group.state === 'interrupted' ? '' : undefined}
                data-no-result={group.state === 'no_result' ? '' : undefined}
                className={cn(
                  'inline-flex items-center gap-1',
                  group.state === 'failed' ? 'text-red-text' : 'text-ink-500',
                )}
              >
                {group.state === 'started' && <ToolSpinner />}
                <span data-tool-text="" className="inline-flex items-baseline gap-1">
                  <span className={cn('font-mono text-[12px] font-medium', group.state === 'succeeded' || group.state === 'started' ? 'text-ink-700' : undefined)}>
                    {group.toolName}
                  </span>
                  {group.state !== 'succeeded' && <span>{label}</span>}
                  {group.count > 1 && (
                    <span className={cn('tabular-nums', group.state === 'failed' ? 'text-red-text/75' : 'text-ink-500/70')}>
                      ×{group.count}
                    </span>
                  )}
                </span>
              </span>
            </li>
          )
        })}
        {after}
      </ul>
    </ToolListLeadingContext.Provider>
  )
}
