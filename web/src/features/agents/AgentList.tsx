import { useId, useSyncExternalStore } from 'react'
import { Link } from 'react-router'
import { AgentAvatar } from '@/components/AgentAvatar'
import { ErrorAlert } from '@/components/ErrorAlert'
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

// At most one status tag on the right of each row, by priority: session lost, replying, sleep, Manager. Nothing is shown when idle.
// On the blue background, sleep and Manager use a semi-transparent navy background; when their row is selected (yellow
// background) they become a pill with white text on solid navy, because the semi-transparent one is hard to read on yellow.
function StatusTag({ agent, current }: { agent: Schemas['AgentListItem']; current: boolean }) {
  const quiet = current ? 'rounded-full bg-ink px-2 text-white' : 'bg-ink/45 text-white'
  const sleeping = !agent.busy && agent.sleep?.state === 'sleeping' && agent.sleep.wakeAt ? agent.sleep : null
  const [label, tone, title] =
    agent.sessionState === 'missing'
      ? [t.agents.sessionMissing, 'bg-tint-red text-red-text', undefined]
      : agent.busy
        ? [text.chips.busy, 'bg-riso-pink text-ink', undefined]
        : sleeping
          ? [text.chips.sleep, quiet, [t.sleep.until(sleeping.wakeAt!), sleeping.reason].filter(Boolean).join(' · ')]
          : agent.isManager
            ? [t.agents.manager, quiet, t.agents.managerTitle]
            : [null, '', undefined]
  if (!label) return null
  return (
    <span title={title} className={cn('shrink-0 rounded-[6px] px-1.5 py-px text-[11px] leading-[18px] font-black whitespace-nowrap', tone)}>
      {label}
    </span>
  )
}

// When the server can't be reached, show a halftone skeleton as a placeholder; the explanatory text is the same as when the
// list request fails.
function Skeleton() {
  return (
    <div className="grid gap-1.5 px-1 pt-1">
      <span aria-hidden="true" className="riso-skeleton h-8 rounded-ticket opacity-35" />
      <span aria-hidden="true" className="riso-skeleton h-8 rounded-ticket opacity-25" />
      <span aria-hidden="true" className="riso-skeleton h-8 rounded-ticket opacity-15" />
      <p role="status" className="px-1.5 text-[12.5px] font-black text-white">
        {t.errors.unreachable}
      </p>
    </div>
  )
}

// The Agent group in the sidebar: a heading (AGENT in a project, platform AGENT in global) and a New button, with this scope's
// Agents below. Each row is an overprinted avatar, the name, a runtime mark (only for a runtime installed on this computer, such as
// Claude Code), a status tag and a “待你回复” (Needs your reply) sticker. Search only affects what is shown, not the periodic refresh or the Agent already open.
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
  // Switch to the halftone skeleton when the server can't be reached and there is no usable list (it never loaded, or the
  // latest refresh failed because the server was unreachable). A list that has loaded stays shown as usual until a later
  // refresh fails, so an occasional failed request doesn't make the list flicker.
  const offline = (!reachable && (!agents.data || isUnreachable(agents.error))) || (!scope && isUnreachable(listError))
  const loading = scope ? agents.isPending : !offline

  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-1">
      <div className={navGroupClass}>
        <h2 id={headingId} className="font-normal">
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
          {loading && <p className="px-2.5 text-[12.5px] text-white/75">{t.common.loading}</p>}
          {agents.isError && <ErrorAlert error={agents.error} className="text-[12.5px]" />}
          {agents.isSuccess && list.length === 0 && <p className="px-2.5 text-[12.5px] text-white/75">{t.agents.empty}</p>}
          {list.length > 0 && shown.length === 0 && (
            <p role="status" className="px-2.5 text-[12.5px] text-white/75">
              {t.agents.search.noMatch}
            </p>
          )}
          {scope && <ul aria-labelledby={headingId} className="flex flex-col gap-1">
            {shown.map((agent) => {
              const current = agent.id === currentAgentId
              return (
                <li key={agent.id} className="min-w-0">
                  <Link to={agentPath(scope, agent.id)} aria-current={current ? 'page' : undefined} className={navRowClass(current, 'agent')}>
                    <AgentAvatar size="sm" id={agent.id} />
                    {/* The name fills the space first: names of usual length are shown in full (also when selected), and the
                        runtime, status and Manager marks on the right wrap to the next line, right-aligned, when they don't fit.
                        Only a name longer than the whole row is truncated; hovering shows the full name. */}
                    <span className="flex min-w-0 flex-1 flex-wrap items-center gap-x-2 gap-y-0.5">
                      <span title={agent.name} className="max-w-full truncate">
                        {agent.name}
                      </span>
                      <span className="ml-auto flex shrink-0 items-center gap-2 empty:hidden">
                        <RuntimeMark runtime={agent.runtime} />
                        <StatusTag agent={agent} current={current} />
                        <PendingBadge sticker counts={pendingByAgent.get(agent.id)} />
                      </span>
                    </span>
                  </Link>
                </li>
              )
            })}
          </ul>}
        </>
      )}
    </section>
  )
}
