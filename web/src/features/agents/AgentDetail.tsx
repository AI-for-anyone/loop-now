import { useCallback, useState } from 'react'
import { Link, Navigate, Outlet, useLocation, useParams } from 'react-router'
import { ErrorAlert } from '@/components/ErrorAlert'
import { IndexTabLink, IndexTabs } from '@/components/IndexTabs'
import { ManagerBadge } from '@/components/ManagerBadge'
import { OverprintDeco } from '@/components/OverprintDeco'
import { RuntimeBadge } from '@/components/RuntimeBadge'
import { Sheet } from '@/components/Sheet'
import type { Schemas } from '@/api/client'
import { useAgent, useAgentStatus, useProjects } from '@/api/queries'
import { config } from '@/config'
import { runPhaseText } from '@/features/chat/run-status-text'
import { useRuntimeLookup, type RuntimeLookup } from '@/lib/runtime'
import { t } from '@/i18n'
import { useScope } from '@/features/projects/scope-context'
import { isNotFound } from '@/lib/errors'
import { agentPath, agentScope, GLOBAL, overviewPath, sameScope } from '@/lib/scope'
import { sheetTitleClass } from '@/lib/page-title'
import { cn } from '@/lib/utils'
import type { AgentOutletContext, AgentStickerState } from './agent-context'
import { AgentAddress } from './AgentAddress'
import { SleepUntil } from '@/features/sleep/SleepUntil'
import { stopAllBackgroundTasks, stopBackgroundTask } from '@/api/background-tasks'
import { BackgroundTasksMark, type BackgroundStopper } from '@/features/chat/BackgroundTasks'

// The model tag in the header: "provider · model", except for a command a person added, whose provider id (acp-<command id>) and one
// model ("default") say nothing: it shows the command's name.
function modelTag(model: Schemas['ModelRef'], runtimes: RuntimeLookup): string {
  const runtime = runtimes.forProvider(model.provider)
  return runtime?.acpCommandId ? runtime.name : `${model.provider} · ${model.modelId}`
}

// The status tag next to the title (an outlined, slightly tilted pill): busy (plus the current phase), or idle. The live status handed over by the Chat tab takes precedence; without it, the periodically polled status endpoint
// is used, and until that result arrives, the busy/idle and session state from the Agent details.
// Background tasks still running get their own mark after it (“后台任务 N”, Background N), which opens the list to see or stop them.
function StatusChip({ agent, live }: { agent: Schemas['Agent']; live: AgentStickerState | null }) {
  const status = useAgentStatus(agent.id, config.agentListPollMs)
  const scope = useScope()
  const polled: AgentStickerState = status.data
    ? {
        busy: status.data.busy,
        phase: status.data.currentRun ? runPhaseText(status.data.currentRun) : null,
        sleep: status.data.sleep,
        backgroundTasks: status.data.backgroundTasks,
      }
    : { busy: agent.busy, phase: null }
  const state = live ?? polled
  const [tone, label] = state.busy ? (['busy', t.agents.busy] as const) : (['success', t.agents.idle] as const)
  const phase = state.busy ? state.phase : null
  return (
    <>
      <span
        role="status"
        data-agent-status
        data-tone={tone}
        className={cn(
          'riso-tilt inline-flex min-h-[26px] max-w-full flex-wrap items-center gap-x-1.5 rounded-full px-3 text-[12.5px] leading-[26px] font-black [--tilt-shadow:inset_0_0_0_2px_var(--ink)] [--tilt:-3deg]',
          chipTone[tone],
        )}
      >
        <span aria-hidden="true" className={cn('size-[7px] shrink-0 rounded-full', chipDot[tone])} />
        <span>{label}</span>
        {phase && <span className="font-medium">{phase}</span>}
      </span>
      {!state.busy && state.sleep?.state === 'sleeping' && state.sleep.wakeAt &&
        <SleepUntil at={state.sleep.wakeAt} reason={state.sleep.reason} />}
      <BackgroundTasksMark
        tasks={state.backgroundTasks ?? []}
        stopper={backgroundStopper(agent.id)}
        // The reply that started a task: located in the conversation, named by its number when the Chat tab has it loaded.
        origin={(task) => {
          const number = state.replyNumbers?.[task.runId]
          return { to: `${agentPath(scope, agent.id, 'chat')}#run-${encodeURIComponent(task.runId)}`, label: number ? t.background.fromReply(number) : t.background.fromReplyAt(t.chat.time(task.startedAt)) }
        }}
      />
    </>
  )
}

const backgroundStopper = (agentId: string): BackgroundStopper => ({
  stop: (taskId, actionId) => stopBackgroundTask(agentId, taskId, actionId),
  stopAll: (actionId) => stopAllBackgroundTasks(agentId, actionId),
})

const chipTone = { success: 'text-green-text [--tilt-bg:var(--tint-green)]', busy: 'text-ink [--tilt-bg:var(--tint-pink)]', danger: 'text-red-text [--tilt-bg:var(--tint-red)]' }
const chipDot = { success: 'bg-green', busy: 'bg-riso-pink', danger: 'bg-red' }

export function AgentDetail() {
  const { agentId = '' } = useParams()
  const location = useLocation()
  const scope = useScope()
  const projects = useProjects()
  const agent = useAgent(agentId)
  const runtimes = useRuntimeLookup()
  // The live status handed over by the Chat tab, recorded together with the Agent it belongs to, so that switching Agents never
  // uses the previous Agent's status.
  const [live, setLive] = useState<{ agentId: string; state: AgentStickerState } | null>(null)
  const reportLiveStatus = useCallback((state: AgentStickerState | null) => setLive(state ? { agentId, state } : null), [agentId])

  if (isNotFound(agent.error)) {
    return <p className="px-6 py-7 text-[15px] text-ink-500" role="status">{t.agents.notFound}</p>
  }
  // Once data exists, a failed background refresh is only reported above the tabs; the tab content is not unmounted, so
  // unsaved edits are not lost.
  if (!agent.data) {
    if (agent.isError) return <ErrorAlert error={agent.error} className="mx-6 my-7 w-auto" />
    return <p className="px-6 py-7 text-[15px] text-ink-500">{t.common.loading}</p>
  }

  // When the project or platform in the URL doesn't match where the Agent actually belongs, switch to the URL where it actually
  // lives, keeping the tab, query parameters and hash unchanged.
  const actual = projects.data ? agentScope(agent.data, projects.data.projects) : null
  if (actual && !sameScope(actual, scope)) {
    const tab = location.pathname.split('/').at(-1)
    const target = agentPath(actual, agentId, tab === 'chat' || tab === 'config' || tab === 'folder' || tab === 'inbox' ? tab : undefined)
    return <Navigate replace to={`${target}${location.search}${location.hash}`} />
  }

  return (
    // The page header and index tabs are on top, and the tab content is in the sheet below. The left, right and bottom padding
    // varies with the window width; the bottom padding leaves room for the sheet's shadow.
    <div className="flex h-full min-h-0 flex-col px-[clamp(14px,2.4vw,34px)] pb-[26px] max-nav:pb-4">
      {/* On wide screens the header is compact (used by all four tabs): a tighter breadcrumb, a 40px title, and the role (Manager),
          status, runtime, model and address in one row to the right of the title, wrapping when they don't fit;
          the decoration shrinks to the header's height. Narrow screens stay as before. */}
      <header className="@container relative flex shrink-0 flex-col gap-3 pt-[22px] pb-[18px] nav:gap-1.5 nav:pt-3 nav:pb-2.5" data-agent-header>
        <OverprintDeco compact />
        <nav aria-label={t.shell.breadcrumb} className="relative">
          <ol className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-[12px] font-bold text-ink-500 nav:leading-4">
            <li className="min-w-0">
              <Link to={overviewPath(scope.kind === 'project' ? { kind: 'project', name: scope.project.name } : GLOBAL)} className="[overflow-wrap:anywhere] hover:text-ink hover:underline">
                {scope.kind === 'project' ? scope.project.name : t.shell.global}
              </Link>
            </li>
            <li aria-hidden="true">›</li>
            <li>{scope.kind === 'project' ? t.shell.agentCrumb : t.shell.platformAgentCrumb}</li>
            <li aria-hidden="true">›</li>
            <li aria-current="page" className="min-w-0 [overflow-wrap:anywhere] text-riso-blue">
              {agent.data.name}
            </li>
          </ol>
        </nav>
        <div
          data-agent-title-row
          className="relative flex min-w-0 flex-col gap-3 @[820px]:pr-[316px] nav:flex-row nav:flex-wrap nav:items-center nav:gap-x-3 nav:gap-y-1.5 nav:@[820px]:pr-[196px]"
        >
          <div className="flex min-w-0 flex-wrap items-center gap-x-3.5 gap-y-2.5 nav:contents">
            <h1 className={cn(sheetTitleClass, 'mr-1.5 nav:text-[40px] nav:leading-none nav:[text-shadow:3px_3px_0_var(--riso-pink)]')}>{agent.data.name}</h1>
            {agent.data.isManager && <ManagerBadge size="md" className="px-3 text-[12.5px] leading-6" />}
            <RuntimeBadge runtime={agent.data.runtime} />
            <StatusChip agent={agent.data} live={live?.agentId === agentId ? live.state : null} />
          </div>
          {/* Model and address: the model is a monospace tag on a light blue background, the address is in a dashed box.
              On narrow screens they get a row of their own. */}
          <div className="flex min-w-0 flex-wrap items-center gap-x-2.5 gap-y-2 text-[12px] nav:contents">
            <span className="min-w-0 rounded-stamp bg-tint-blue px-[9px] font-mono text-[12px] leading-[22px] [overflow-wrap:anywhere]">{modelTag(agent.data.model, runtimes)}</span>
            <AgentAddress address={agent.data.address} />
          </div>
        </div>
      </header>
      <IndexTabs aria-label={agent.data.name}>
        <IndexTabLink to={agentPath(scope, agentId, 'chat')}>{t.agents.tabs.chat}</IndexTabLink>
        <IndexTabLink to={agentPath(scope, agentId, 'config')}>{t.agents.tabs.config}</IndexTabLink>
        <IndexTabLink to={agentPath(scope, agentId, 'folder')}>{t.agents.tabs.folder}</IndexTabLink>
        <IndexTabLink to={agentPath(scope, agentId, 'inbox')}>{t.agents.tabs.inbox}</IndexTabLink>
      </IndexTabs>
      <Sheet className="flex-1">
        {agent.isError && <ErrorAlert error={agent.error} className="mx-6 mt-3 w-auto" />}
        <div className="min-h-0 flex-1">
          {/* Remount the tabs per Agent: drafts, dialogs, live status and scroll position must not carry over to another Agent. */}
          <Outlet key={agentId} context={{ agent: agent.data, reportLiveStatus } satisfies AgentOutletContext} />
        </div>
      </Sheet>
    </div>
  )
}
