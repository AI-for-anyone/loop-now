import { MoonIcon, SquareTerminalIcon } from 'lucide-react'
import { useId, useSyncExternalStore } from 'react'
import { Link } from 'react-router'
import { AgentAvatar } from '@/components/AgentAvatar'
import { ErrorAlert } from '@/components/ErrorAlert'
import { ManagerFlag } from '@/components/ManagerBadge'
import { NavIcon } from '@/components/NavIcon'
import { RuntimeMark } from '@/components/RuntimeBadge'
import { reachability, type Schemas } from '@/api/client'
import { useInboxPending } from '@/api/console-inbox'
import { useAgents } from '@/api/queries'
import { PendingBadge } from '@/features/console-inbox/PendingBadge'
import { t } from '@/i18n'
import { isUnreachable } from '@/lib/errors'
import { navAddClass, navGroupClass, navRowClass } from '@/lib/nav-row'
import { agentPath, scopeKey, type Scope } from '@/lib/scope'
import { cn } from '@/lib/utils'

const text = t.shell

// The text that search matches against: the display name, plus the address when the server provides one.
function searchableText(agent: Schemas['AgentListItem']): string[] {
  const address = 'address' in agent && typeof agent.address === 'string' ? [agent.address] : []
  return [agent.name, ...address]
}

// Case-insensitive substring match; the search term is trimmed of leading and trailing whitespace. An empty term matches everything.
function matchesAgent(agent: Schemas['AgentListItem'], search: string): boolean {
  const keyword = search.trim().toLowerCase()
  return keyword === '' || searchableText(agent).some((text) => text.toLowerCase().includes(keyword))
}

// At most one compact status symbol on the right of each row, by priority: replying (pulsing dot), background tasks still running
// (small pill with the shell glyph and count), sleep (moon glyph), Manager (flag glyph). Full words go in the tooltip and
// screen-reader text. Nothing is shown when idle. Colors adapt via semantic sidebar tokens so selected and unselected rows stay high
// contrast across every theme.
function StatusTag({ agent, current }: { agent: Schemas['AgentListItem']; current: boolean }) {
  if (agent.busy) {
    return (
      <span data-status="busy" title={text.chips.busy} className="inline-flex size-[18px] shrink-0 items-center justify-center">
        <span
          aria-hidden="true"
          data-status-dot
          className={cn(
            'size-2.5 rounded-full bg-status-busy animate-pulse motion-reduce:animate-none',
            current && 'ring-1 ring-sidebar-active-fg/40',
          )}
        />
        <span className="sr-only">{text.chips.busy}</span>
      </span>
    )
  }
  if (agent.backgroundTaskCount > 0) {
    const label = t.background.listTitle(agent.backgroundTaskCount)
    return (
      <span
        data-status="background"
        title={label}
        className={cn(
          'inline-flex h-[18px] shrink-0 items-center gap-0.5 rounded-full px-1.5 text-[11px] leading-none font-semibold tabular-nums whitespace-nowrap',
          current ? 'bg-sidebar-active-fg text-sidebar-active-bg' : 'bg-sidebar-active-bg text-sidebar-active-fg',
        )}
      >
        <SquareTerminalIcon aria-hidden="true" className="size-3 shrink-0" strokeWidth={2.4} />
        <span aria-hidden="true">{agent.backgroundTaskCount}</span>
        <span className="sr-only">{label}</span>
      </span>
    )
  }
  const quiet = current ? 'bg-sidebar-active-fg text-sidebar-active-bg' : 'bg-sidebar-control-bg text-sidebar-control-fg'
  const sleeping = agent.sleep?.state === 'sleeping' && agent.sleep.wakeAt ? agent.sleep : null
  if (sleeping) {
    const label = [t.sleep.until(sleeping.wakeAt!), sleeping.reason].filter(Boolean).join(' · ')
    return (
      <span
        data-status="sleeping"
        title={label}
        className={cn('inline-flex size-[18px] shrink-0 items-center justify-center rounded-full', quiet)}
      >
        <MoonIcon aria-hidden="true" className="size-3 shrink-0" strokeWidth={2.4} />
        <span className="sr-only">{label}</span>
      </span>
    )
  }
  if (agent.isManager) {
    return (
      <span
        data-status="manager"
        title={t.agents.managerTitle}
        className={cn('inline-flex size-[18px] shrink-0 items-center justify-center rounded-full', quiet)}
      >
        <ManagerFlag className="size-3 shrink-0" />
        <span className="sr-only">{t.agents.managerTitle}</span>
      </span>
    )
  }
  return null
}

// When the server can't be reached, show a skeleton as a placeholder; the explanatory text is the same as when the
// list request fails.
function Skeleton() {
  return (
    <div className="grid gap-1.5 px-1 pt-1">
      <span aria-hidden="true" className="riso-skeleton h-8 rounded-ticket opacity-35" />
      <span aria-hidden="true" className="riso-skeleton h-8 rounded-ticket opacity-25" />
      <span aria-hidden="true" className="riso-skeleton h-8 rounded-ticket opacity-15" />
      <p role="status" className="px-1.5 text-[12.5px] font-semibold text-sidebar-fg">
        {t.errors.unreachable}
      </p>
    </div>
  )
}

// The Agent group in the sidebar: a heading (Agents in a project, Platform Agents in global) and a New button, with this scope's
// Agents below. Each row stays on one line with an avatar, truncated name, runtime mark (for local runtimes such as Claude Code),
// compact status symbol and pending reply badge. Search only affects what is shown, not the periodic refresh or the Agent already open.
// While the project list hasn't loaded yet, scope is null: show only "loading", or the skeleton when the project list failed
// because the server can't be reached (listError).
export function AgentNavGroup({
  scope,
  title,
  search,
  currentAgentId,
  onCreate,
  listError,
}: {
  scope: Scope | null
  listError?: unknown
  title: string
  search: string
  currentAgentId: string | null
  onCreate: () => void
}) {
  const agents = useAgents(scope && scopeKey(scope))
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  const headingId = useId()
  const list = agents.data?.agents ?? []
  const shown = list.filter((agent) => matchesAgent(agent, search))
  // Needs your reply: questions and new messages this Agent sent you that haven't been handled yet (not counting its task instances).
  const pending = useInboxPending()
  const pendingByAgent = new Map((pending.data?.byAgent ?? []).map((row) => [row.agentId, row]))
  // Switch to the skeleton when the server can't be reached and there is no usable list (it never loaded, or the
  // latest refresh failed because the server was unreachable). A list that has loaded stays shown as usual until a later
  // refresh fails, so an occasional failed request doesn't make the list flicker.
  const offline = (!reachable && (!agents.data || isUnreachable(agents.error))) || (!scope && isUnreachable(listError))
  const loading = scope ? agents.isPending : !offline

  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-1">
      <div className={navGroupClass}>
        <h2 id={headingId} title={title} className="min-w-0 truncate font-normal">
          {title}
        </h2>
        <button type="button" aria-label={t.agents.create} disabled={!reachable || !scope} onClick={onCreate} className={navAddClass}>
          <NavIcon name="plus" className="size-3 [stroke-width:3]" />
        </button>
      </div>
      {offline ? (
        <Skeleton />
      ) : (
        <>
          {loading && <p className="px-2.5 text-[12.5px] text-sidebar-muted">{t.common.loading}</p>}
          {agents.isError && <ErrorAlert error={agents.error} className="text-[12.5px]" />}
          {agents.isSuccess && list.length === 0 && <p className="px-2.5 text-[12.5px] text-sidebar-muted">{t.agents.empty}</p>}
          {list.length > 0 && shown.length === 0 && (
            <p role="status" className="px-2.5 text-[12.5px] text-sidebar-muted">
              {t.agents.search.noMatch}
            </p>
          )}
          {scope && (
            <ul aria-labelledby={headingId} className="flex flex-col gap-1">
              {shown.map((agent) => {
                const current = agent.id === currentAgentId
                return (
                  <li key={agent.id} className="min-w-0">
                    <Link to={agentPath(scope, agent.id)} aria-current={current ? 'page' : undefined} className={navRowClass(current, 'agent')}>
                      <AgentAvatar size="sm" id={agent.id} busy={agent.busy} sleeping={!agent.busy && agent.sleep?.state === 'sleeping'} />
                      {/* One line at every sidebar width: the name gives way first with an ellipsis (full name in the tooltip), while the
                          compact runtime, status and pending marks stay right-aligned. */}
                      <span title={agent.name} className="min-w-0 flex-1 truncate">
                        {agent.name}
                      </span>
                      <span className="flex shrink-0 items-center gap-1.5 empty:hidden">
                        <RuntimeMark runtime={agent.runtime} />
                        <StatusTag agent={agent} current={current} />
                        <PendingBadge sticker counts={pendingByAgent.get(agent.id)} />
                      </span>
                    </Link>
                  </li>
                )
              })}
            </ul>
          )}
        </>
      )}
    </section>
  )
}
