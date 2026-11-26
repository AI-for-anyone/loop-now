import { useCallback, useState } from 'react'
import { Link, NavLink, Navigate, Outlet, useLocation, useParams } from 'react-router'
import { AgentAvatar } from '@/components/AgentAvatar'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ManagerBadge } from '@/components/ManagerBadge'
import { OverprintDeco } from '@/components/OverprintDeco'
import { SidebarExpandButton } from '@/components/PageHeader'
import { Sheet } from '@/components/Sheet'
import { useSidebarControl } from '@/app/sidebar-context'
import { hasInsetTitleBar } from '@/features/desktop/desktop'
import type { Schemas } from '@/api/client'
import { useAgent, useAgentStatus, useProjects } from '@/api/queries'
import { config } from '@/config'
import { runPhaseText } from '@/features/chat/run-status-text'
import { useRuntimeLookup, type RuntimeLookup } from '@/lib/runtime'
import { t } from '@/i18n'
import { useScope } from '@/features/projects/scope-context'
import { isNotFound } from '@/lib/errors'
import { agentPath, agentScope, GLOBAL, overviewPath, sameScope } from '@/lib/scope'
import { cn } from '@/lib/utils'
import type { AgentOutletContext, AgentStickerState } from './agent-context'
import { AgentAddress } from './AgentAddress'
import { SleepUntil } from '@/features/sleep/SleepUntil'
import { stopAllBackgroundTasks, stopBackgroundTask } from '@/api/background-tasks'
import { BackgroundTasksMark, type BackgroundStopper } from '@/features/chat/BackgroundTasks'

// The model tag shown subtly on non-Chat tabs: "provider · model", except for a command a person added, whose provider id
// (acp-<command id>) and one model ("default") say nothing: it shows the command's name.
function modelTag(model: Schemas['ModelRef'], runtimes: RuntimeLookup): string {
  const runtime = runtimes.forProvider(model.provider)
  return runtime?.acpCommandId ? runtime.name : `${model.provider} · ${model.modelId}`
}

const backgroundStopper = (agentId: string): BackgroundStopper => ({
  stop: (taskId, actionId) => stopBackgroundTask(agentId, taskId, actionId),
  stopAll: (actionId) => stopAllBackgroundTasks(agentId, actionId),
})

const chipTone = {
  success:
    'border-transparent bg-transparent px-1.5 text-ink-500 [[data-skin=plate]_&]:px-2.5 [[data-skin=plate]_&]:text-green-text [[data-skin=plate]_&]:[--tilt-bg:var(--tint-green)]',
  busy:
    'border-hairline-strong bg-surface-active px-2 text-ink [[data-skin=plate]_&]:px-2.5 [[data-skin=plate]_&]:[--tilt-bg:var(--tint-pink)]',
  danger:
    'border-red-text/30 bg-tint-red px-2 text-red-text [[data-skin=plate]_&]:px-2.5 [[data-skin=plate]_&]:[--tilt-bg:var(--tint-red)]',
}

const chipDot = {
  success: 'bg-green',
  busy: 'bg-ink [[data-skin=plate]_&]:bg-riso-pink',
  danger: 'bg-red',
}

const SEGMENT_BASE =
  'inline-flex h-7 items-center rounded-ticket px-2.5 text-[12.5px] whitespace-nowrap transition-colors outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:h-8 [[data-skin=plate]_&]:rounded-t-ticket [[data-skin=plate]_&]:rounded-b-none [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:px-3'

function segmentClass(isActive: boolean): string {
  return cn(
    SEGMENT_BASE,
    isActive
      ? 'bg-surface-elevated font-semibold text-ink shadow-xs [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)]'
      : 'font-medium text-ink-500 hover:text-ink [[data-skin=plate]_&]:bg-paper-tab [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-ink-700 [[data-skin=plate]_&]:hover:bg-paper-raised',
  )
}

// Single-row 48px workspace top bar: sidebar expand + breadcrumb + dither avatar + title + status + segmented tabs + compact address.
// Polled status lives here so both the animated dither avatar and the compact status chip react immediately to live/polled state.
function AgentWorkspaceHeader({
  agent,
  live,
  isChatTab,
}: {
  agent: Schemas['Agent']
  live: AgentStickerState | null
  isChatTab: boolean
}) {
  const scope = useScope()
  const runtimes = useRuntimeLookup()
  const { collapsed } = useSidebarControl()
  const status = useAgentStatus(agent.id, config.agentListPollMs)
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
  const sleeping = !state.busy && state.sleep?.state === 'sleeping' && Boolean(state.sleep.wakeAt)
  const desktop = hasInsetTitleBar()

  return (
    <header
      data-agent-header
      className="@container relative flex shrink-0 flex-col [[data-skin=plate]_&]:gap-2.5 [[data-skin=plate]_&]:pt-[18px] [[data-skin=plate]_&]:pb-3 [[data-skin=plate]_&]:nav:pt-0 [[data-skin=plate]_&]:nav:pb-2.5"
    >
      <OverprintDeco compact />
      <div
        data-agent-title-row
        className={cn(
          'relative flex min-h-12 flex-wrap items-center gap-x-2.5 gap-y-1.5 border-b border-hairline bg-surface-canvas px-4 py-1.5 sm:h-12 sm:flex-nowrap sm:py-0',
          desktop && 'window-drag',
          collapsed && desktop && 'nav:pl-[84px]',
          '[[data-skin=plate]_&]:h-auto [[data-skin=plate]_&]:min-h-12 [[data-skin=plate]_&]:flex-wrap [[data-skin=plate]_&]:border-b-0 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:px-0 [[data-skin=plate]_&]:py-0 [[data-skin=plate]_&]:@[820px]:pr-[316px] [[data-skin=plate]_&]:nav:@[820px]:pr-[196px]',
          collapsed && desktop && '[[data-skin=plate]_&]:nav:pl-[calc(84px-clamp(14px,2.4vw,34px))]',
        )}
      >
        <div className="flex min-w-0 flex-1 items-center gap-2.5">
          <SidebarExpandButton />
          <nav aria-label={t.shell.breadcrumb} className="min-w-0 shrink-0 max-md:sr-only">
            <ol className="flex min-w-0 items-center gap-1.5 text-[12px] font-medium text-ink-500 [[data-skin=plate]_&]:font-bold">
              <li className="min-w-0 max-w-[140px] truncate">
                <Link
                  to={overviewPath(scope.kind === 'project' ? { kind: 'project', name: scope.project.name } : GLOBAL)}
                  className="window-no-drag hover:text-ink hover:underline"
                >
                  {scope.kind === 'project' ? scope.project.name : t.shell.global}
                </Link>
              </li>
              <li aria-hidden="true" className="text-ink-500/60">/</li>
              <li aria-current="page" className="sr-only">
                {agent.name}
              </li>
            </ol>
          </nav>
          <AgentAvatar id={agent.id} name={agent.name} size="sm" busy={state.busy} sleeping={sleeping} />
          <h1 className="min-w-0 truncate text-[14px] font-semibold tracking-[-0.01em] text-ink [[data-skin=plate]_&]:overflow-visible [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[32px] [[data-skin=plate]_&]:leading-none [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal [[data-skin=plate]_&]:text-clip [[data-skin=plate]_&]:whitespace-normal [[data-skin=plate]_&]:text-riso-blue [[data-skin=plate]_&]:[text-shadow:3px_3px_0_var(--riso-pink)]">
            {agent.name}
          </h1>
          {agent.isManager && <ManagerBadge size="sm" className="shrink-0" />}
          <span
            role="status"
            data-agent-status
            data-tone={tone}
            className={cn(
              'inline-flex h-6 shrink-0 items-center gap-1.5 rounded-full border text-[12px] leading-none font-medium whitespace-nowrap [[data-skin=plate]_&]:riso-tilt [[data-skin=plate]_&]:min-h-[26px] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:[--tilt-shadow:inset_0_0_0_2px_var(--ink)] [[data-skin=plate]_&]:[--tilt:-3deg]',
              chipTone[tone],
            )}
          >
            <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full [[data-skin=plate]_&]:size-[7px]', chipDot[tone])} />
            <span>{label}</span>
            {phase && <span className="max-w-[160px] truncate font-normal text-ink-700 [[data-skin=plate]_&]:font-medium">{phase}</span>}
          </span>
          {sleeping && state.sleep?.wakeAt && (
            <div className="min-w-0 max-w-[200px]">
              <SleepUntil at={state.sleep.wakeAt} reason={state.sleep.reason} compact />
            </div>
          )}
          <BackgroundTasksMark
            tasks={state.backgroundTasks ?? []}
            stopper={backgroundStopper(agent.id)}
            // The reply that started a task: located in the conversation, named by its number when the Chat tab has it loaded.
            origin={(task) => {
              const number = state.replyNumbers?.[task.runId]
              return {
                to: `${agentPath(scope, agent.id, 'chat')}#run-${encodeURIComponent(task.runId)}`,
                label: number ? t.background.fromReply(number) : t.background.fromReplyAt(t.chat.time(task.startedAt)),
              }
            }}
          />
        </div>
        <div className="window-no-drag ml-auto flex shrink-0 items-center gap-2 max-sm:w-full max-sm:justify-between">
          {!isChatTab && (
            <span className="hidden shrink-0 items-center rounded-stamp border border-hairline bg-surface-subtle px-2 py-0.5 font-mono text-[11px] leading-4 text-ink-500 xl:inline-flex [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue [[data-skin=plate]_&]:text-ink">
              {modelTag(agent.model, runtimes)}
            </span>
          )}
          <nav
            aria-label={agent.name}
            className="inline-flex h-8 shrink-0 items-center rounded-input border border-hairline bg-surface-subtle p-0.5 [[data-skin=plate]_&]:h-auto [[data-skin=plate]_&]:gap-1 [[data-skin=plate]_&]:rounded-none [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-transparent [[data-skin=plate]_&]:p-0"
          >
            <NavLink to={agentPath(scope, agent.id, 'chat')} className={({ isActive }) => segmentClass(isActive)}>
              {t.agents.tabs.chat}
            </NavLink>
            <NavLink to={agentPath(scope, agent.id, 'config')} className={({ isActive }) => segmentClass(isActive)}>
              {t.agents.tabs.config}
            </NavLink>
            <NavLink to={agentPath(scope, agent.id, 'folder')} className={({ isActive }) => segmentClass(isActive)}>
              {t.agents.tabs.folder}
            </NavLink>
            <NavLink to={agentPath(scope, agent.id, 'inbox')} className={({ isActive }) => segmentClass(isActive)}>
              {t.agents.tabs.inbox}
            </NavLink>
          </nav>
          <AgentAddress address={agent.address} variant="compact" />
        </div>
      </div>
    </header>
  )
}

export function AgentDetail() {
  const { agentId = '' } = useParams()
  const location = useLocation()
  const scope = useScope()
  const projects = useProjects()
  const agent = useAgent(agentId)
  // The live status handed over by the Chat tab, recorded together with the Agent it belongs to, so that switching Agents never
  // uses the previous Agent's status.
  const [live, setLive] = useState<{ agentId: string; state: AgentStickerState } | null>(null)
  const reportLiveStatus = useCallback((state: AgentStickerState | null) => setLive(state ? { agentId, state } : null), [agentId])

  if (isNotFound(agent.error)) {
    return <p className="px-6 py-7 text-[15px] text-ink-500" role="status">{t.agents.notFound}</p>
  }
  // Once data exists, a failed background refresh is only reported above the tab content; the tab content is not unmounted, so
  // unsaved edits are not lost.
  if (!agent.data) {
    if (agent.isError) return <ErrorAlert error={agent.error} className="mx-6 my-7 w-auto" />
    return <p className="px-6 py-7 text-[15px] text-ink-500">{t.common.loading}</p>
  }

  // When the project or platform in the URL doesn't match where the Agent actually belongs, switch to the URL where it actually
  // lives, keeping the tab, query parameters and hash unchanged.
  const actual = projects.data ? agentScope(agent.data, projects.data.projects) : null
  const tab = location.pathname.split('/').at(-1)
  if (actual && !sameScope(actual, scope)) {
    const target = agentPath(actual, agentId, tab === 'chat' || tab === 'config' || tab === 'folder' || tab === 'inbox' ? tab : undefined)
    return <Navigate replace to={`${target}${location.search}${location.hash}`} />
  }

  return (
    // Full-bleed workspace shell in mono skin; Plate skin keeps fluid outer padding and bottom room for the sheet shadow.
    <div className="flex h-full min-h-0 flex-col p-0 [[data-skin=plate]_&]:px-[clamp(14px,2.4vw,34px)] [[data-skin=plate]_&]:pb-[26px] [[data-skin=plate]_&]:max-nav:pb-4">
      <AgentWorkspaceHeader
        agent={agent.data}
        live={live?.agentId === agentId ? live.state : null}
        isChatTab={tab === 'chat' || tab === agentId}
      />
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
