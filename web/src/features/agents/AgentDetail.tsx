import { useCallback, useState } from 'react'
import { Link, NavLink, Navigate, Outlet, useLocation, useParams } from 'react-router'
import { ArrowLeftIcon, EllipsisIcon } from 'lucide-react'
import { Popover as PopoverPrimitive } from 'radix-ui'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ManagerBadge } from '@/components/ManagerBadge'
import { SidebarExpandButton } from '@/components/PageHeader'
import { RuntimeMark } from '@/components/RuntimeBadge'
import { Sheet } from '@/components/Sheet'
import { useSidebarControl } from '@/app/sidebar-context'
import { hasInsetTitleBar } from '@/features/desktop/desktop'
import type { Schemas } from '@/api/client'
import { useAgent, useAgentStatus, useProjects } from '@/api/queries'
import { config } from '@/config'
import { runPhaseText } from '@/features/chat/run-status-text'
import { useRuntimeLookup } from '@/lib/runtime'
import { t } from '@/i18n'
import { useScope } from '@/features/projects/scope-context'
import { isNotFound } from '@/lib/errors'
import { agentPath, agentScope, GLOBAL, overviewPath, sameScope, type Scope } from '@/lib/scope'
import { cn } from '@/lib/utils'
import type { AgentOutletContext, AgentStickerState } from './agent-context'
import { AgentAddress } from './AgentAddress'
import { SleepUntil } from '@/features/sleep/SleepUntil'

// Compact status indicator in the header bar: a colored dot plus Busy (and the current phase) or Idle.
function StatusDot({ state }: { state: AgentStickerState }) {
  const [tone, label] = state.busy ? (['busy', t.agents.busy] as const) : (['success', t.agents.idle] as const)
  const phase = state.busy ? state.phase : null
  return (
    <span
      role="status"
      data-agent-status
      data-tone={tone}
      className={cn(
        'inline-flex min-w-0 shrink items-center gap-1.5 text-[13px] leading-5 font-semibold whitespace-nowrap',
        tone === 'busy' ? 'text-pink-text' : 'text-green-text',
      )}
    >
      <span aria-hidden="true" className={cn('size-[7px] shrink-0 rounded-full', tone === 'busy' ? 'bg-status-busy' : 'bg-green')} />
      <span className="shrink-0">{label}</span>
      {phase && <span className="min-w-0 truncate font-medium text-ink-700">{phase}</span>}
    </span>
  )
}

// Compact segmented view switcher tabs in the header bar.
const tabClass = ({ isActive }: { isActive: boolean }) =>
  cn(
    'inline-flex h-7 shrink-0 items-center rounded-ticket px-2.5 text-[13.5px] leading-none whitespace-nowrap no-underline transition-colors outline-none focus-visible:shadow-focus',
    isActive
      ? 'bg-surface-elevated font-semibold text-ink shadow-surface'
      : 'font-medium text-ink-500 hover:text-ink',
  )

// The "..." menu on the right of the header bar: holds the Inbox address (kept visible with its copy button), the runtime and
// model in words, and the link back to the project (or Global for a platform Agent).
function AgentHeaderMenu({ agent, scope, className }: { agent: Schemas['Agent']; scope: Scope; className?: string }) {
  const [open, setOpen] = useState(false)
  const runtimes = useRuntimeLookup()
  const definition = runtimes.get(agent.runtime)
  const providerRuntime = runtimes.forProvider(agent.model.provider)
  // A local ACP command a person added has only its program's own "default" model and a synthetic provider id, so showing a
  // separate model line would say nothing useful; its name is already on the Runtime line.
  const isAddedCommand = Boolean(definition?.acpCommandId || providerRuntime?.acpCommandId)
  const runtimeText = definition
    ? t.agents.menu.runtimeValue(definition.name, definition.capabilities.bundled ? t.runtime.picker.chip.bundled : t.runtime.picker.chip.local)
    : runtimes.name(agent.runtime)
  const backTarget = overviewPath(scope.kind === 'project' ? { kind: 'project', name: scope.project.name } : GLOBAL)
  const backLabel = scope.kind === 'project' ? t.agents.menu.backToProject(scope.project.name) : t.agents.menu.backToGlobal

  return (
    <PopoverPrimitive.Root open={open} onOpenChange={setOpen}>
      <PopoverPrimitive.Trigger
        type="button"
        aria-label={t.agents.menu.label}
        title={t.agents.menu.label}
        className={cn(
          'inline-flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-ticket text-ink-500 transition-colors outline-none hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus aria-expanded:bg-surface-active aria-expanded:text-ink',
          className,
        )}
      >
        <EllipsisIcon aria-hidden="true" className="size-4" strokeWidth={2.2} />
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          side="bottom"
          align="end"
          sideOffset={8}
          collisionPadding={12}
          aria-label={t.agents.menu.title}
          className="z-50 flex w-[min(340px,calc(100vw-24px))] flex-col gap-3 rounded-card border border-hairline-strong bg-surface-elevated p-3.5 text-ink shadow-floating outline-none"
        >
          <div className="flex min-w-0 flex-col gap-1.5">
            <span className="text-[11.5px] leading-none font-semibold text-ink-500">{t.agents.address.label}</span>
            <AgentAddress address={agent.address} showSrLabel={false} />
          </div>
          <dl className="grid gap-2 border-t border-hairline pt-2.5 text-[12.5px]">
            <div className="flex min-w-0 flex-col gap-0.5">
              <dt className="text-[11.5px] leading-none font-semibold text-ink-500">{t.agents.menu.runtime}</dt>
              <dd className="min-w-0 font-medium [overflow-wrap:anywhere] text-ink">{runtimeText}</dd>
            </div>
            {!isAddedCommand && (
              <div className="flex min-w-0 flex-col gap-0.5">
                <dt className="text-[11.5px] leading-none font-semibold text-ink-500">{t.agents.menu.model}</dt>
                <dd className="min-w-0 font-mono text-[12px] [overflow-wrap:anywhere] text-ink-700">{`${agent.model.provider} · ${agent.model.modelId}`}</dd>
              </div>
            )}
          </dl>
          <div className="border-t border-hairline pt-2">
            <Link
              to={backTarget}
              onClick={() => setOpen(false)}
              className="inline-flex w-full items-center gap-2 rounded-ticket px-2 py-1.5 text-[13px] font-medium text-ink no-underline transition-colors outline-none hover:bg-surface-hover focus-visible:shadow-focus"
            >
              <ArrowLeftIcon aria-hidden="true" className="size-3.5 shrink-0 text-ink-500" strokeWidth={2.2} />
              <span className="min-w-0 truncate">{backLabel}</span>
            </Link>
          </div>
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}

function AgentDetailView({
  agent,
  scope,
  refreshError,
  live,
  reportLiveStatus,
}: {
  agent: Schemas['Agent']
  scope: Scope
  refreshError: unknown
  live: AgentStickerState | null
  reportLiveStatus: (state: AgentStickerState | null) => void
}) {
  const { collapsed } = useSidebarControl()
  const desktop = hasInsetTitleBar()
  // The live status handed over by the Chat tab takes precedence; without it, use the periodically polled status endpoint,
  // and until that arrives, the busy/idle state from the Agent details.
  const status = useAgentStatus(agent.id, config.agentListPollMs)
  const polled: AgentStickerState = status.data
    ? {
        busy: status.data.busy,
        phase: status.data.currentRun ? runPhaseText(status.data.currentRun) : null,
        sleep: status.data.sleep,
      }
    : { busy: agent.busy, phase: null }
  const statusState = live ?? polled
  const sleeping = !statusState.busy && statusState.sleep?.state === 'sleeping' && statusState.sleep.wakeAt ? statusState.sleep : null

  return (
    // One thin header bar (48px on wide screens) directly above the full-bleed sheet shared by all four tabs.
    // On phone width it becomes two short lines (name + status + "..." on row 1; horizontally scrollable tabs on row 2),
    // while keeping DOM and keyboard focus order as: name area, tabs, "...".
    <div className="flex h-full min-h-0 flex-col">
      <header
        data-agent-header
        className={cn(
          'grid shrink-0 grid-cols-[minmax(0,1fr)_auto] items-center gap-x-2 gap-y-1.5 border-b border-hairline bg-surface-canvas px-3.5 py-1.5 nav:flex nav:h-12 nav:flex-nowrap nav:items-center nav:gap-3 nav:px-6 nav:py-0',
          desktop && 'window-drag',
          collapsed && desktop && 'nav:pl-[84px]',
        )}
      >
        <div data-agent-identity className="col-start-1 row-start-1 flex min-w-0 items-center gap-2 nav:flex-1">
          <SidebarExpandButton />
          <RuntimeMark runtime={agent.runtime} />
          <h1 title={agent.name} className="min-w-0 truncate text-[16px] leading-6 font-semibold tracking-[-0.01em] text-ink">
            {agent.name}
          </h1>
          {agent.isManager && <ManagerBadge size="sm" className="shrink-0" />}
          <StatusDot state={statusState} />
        </div>
        <nav
          aria-label={agent.name}
          data-agent-tabs
          className="window-no-drag col-span-2 row-start-2 flex items-center gap-0.5 max-nav:overflow-x-auto max-nav:no-scrollbar nav:inline-flex nav:h-8 nav:shrink-0 nav:rounded-input nav:border nav:border-hairline nav:bg-surface-subtle nav:p-0.5"
        >
          <NavLink to={agentPath(scope, agent.id, 'chat')} className={tabClass}>{t.agents.tabs.chat}</NavLink>
          <NavLink to={agentPath(scope, agent.id, 'config')} className={tabClass}>{t.agents.tabs.config}</NavLink>
          <NavLink to={agentPath(scope, agent.id, 'folder')} className={tabClass}>{t.agents.tabs.folder}</NavLink>
          <NavLink to={agentPath(scope, agent.id, 'inbox')} className={tabClass}>{t.agents.tabs.inbox}</NavLink>
        </nav>
        <AgentHeaderMenu key={agent.id} agent={agent} scope={scope} className="window-no-drag col-start-2 row-start-1 nav:shrink-0" />
      </header>
      <Sheet className="flex-1">
        {sleeping && sleeping.wakeAt && (
          <div className="shrink-0 border-b border-hairline bg-surface-subtle/70 px-3.5 py-2 nav:px-6">
            <SleepUntil compact at={sleeping.wakeAt} reason={sleeping.reason} />
          </div>
        )}
        {refreshError !== null && <ErrorAlert error={refreshError} className="mx-3.5 mt-3 w-auto nav:mx-6" />}
        <div className="min-h-0 flex-1">
          {/* Remount the tabs per Agent: drafts, dialogs, live status and scroll position must not carry over to another Agent. */}
          <Outlet key={agent.id} context={{ agent, reportLiveStatus } satisfies AgentOutletContext} />
        </div>
      </Sheet>
    </div>
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
  // Once data exists, a failed background refresh is only reported above the tab panel; the tab content is not unmounted, so
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
    <AgentDetailView
      agent={agent.data}
      scope={scope}
      refreshError={agent.isError ? agent.error : null}
      live={live?.agentId === agentId ? live.state : null}
      reportLiveStatus={reportLiveStatus}
    />
  )
}
