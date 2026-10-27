import { useId, useSyncExternalStore } from 'react'
import { Link } from 'react-router'
import { AgentAvatar } from '@/components/AgentAvatar'
import { ErrorAlert } from '@/components/ErrorAlert'
import { NavIcon } from '@/components/NavIcon'
import { reachability, type Schemas } from '@/api/client'
import { useInboxPending } from '@/api/console-inbox'
import { useAgents } from '@/api/queries'
import { PendingBadge } from '@/features/console-inbox/PendingBadge'
import { t } from '@/i18n'
import { shellText as text } from '@/i18n/shell'
import { isUnreachable } from '@/lib/errors'
import { navAddClass, navGroupClass, navRowClass } from '@/lib/nav-row'
import { agentPath, scopeKey, type Scope } from '@/lib/scope'
import { cn } from '@/lib/utils'

// 参与搜索的文字：显示名称，以及服务端提供时的地址。
function searchableText(agent: Schemas['AgentListItem']): string[] {
  const address = 'address' in agent && typeof agent.address === 'string' ? [agent.address] : []
  return [agent.name, ...address]
}

// 不区分大小写的包含匹配，搜索词去掉首尾空白；搜索词为空时全部匹配。
function matchesAgent(agent: Schemas['AgentListItem'], search: string): boolean {
  const keyword = search.trim().toLowerCase()
  return keyword === '' || searchableText(agent).some((text) => text.toLowerCase().includes(keyword))
}

// 每行右侧最多一个状态标签，按会话丢失、回复中、sleep、Manager 的优先级；空闲时不显示。
function StatusTag({ agent }: { agent: Schemas['AgentListItem'] }) {
  const sleeping = !agent.busy && agent.sleep?.state === 'sleeping' && agent.sleep.wakeAt ? agent.sleep : null
  const [label, tone, title] =
    agent.sessionState === 'missing'
      ? [t.agents.sessionMissing, 'bg-tint-red text-red-text', undefined]
      : agent.busy
        ? [text.chips.busy, 'bg-riso-pink text-ink', undefined]
        : sleeping
          ? [text.chips.sleep, 'bg-ink/45 text-white', [t.sleep.until(sleeping.wakeAt!), sleeping.reason].filter(Boolean).join(' · ')]
          : agent.isManager
            ? [t.agents.manager, 'bg-ink/45 text-white', t.agents.managerTitle]
            : [null, '', undefined]
  if (!label) return null
  return (
    <span title={title} className={cn('shrink-0 rounded-[6px] px-1.5 py-px text-[10.5px] leading-4 font-bold whitespace-nowrap', tone)}>
      {label}
    </span>
  )
}

// 无法连接时用网点骨架占位，说明文字与列表请求失败时相同。
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

// 左侧栏的 Agent 分组：标题（项目里是 AGENT，全局是平台 AGENT）与新建按钮，下面是这个范围的 Agent。
// 每行是叠印头像、名称、状态标签与“待你回复”的贴纸；搜索只影响显示，不影响定期刷新与已打开的 Agent。
// 项目列表还没加载出来时 scope 为 null：只显示加载中，或项目列表因无法连接而失败时的骨架（listError）。
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
  // 待你回复：这个 Agent 发给你、还没处理的问题与新消息（不含它的任务实例）。
  const pending = useInboxPending()
  const pendingByAgent = new Map((pending.data?.byAgent ?? []).map((row) => [row.agentId, row]))
  // 无法连接且没有可用的列表（从未加载成功，或最近一次刷新因无法连接而失败）时改为网点骨架；
  // 已加载的列表在下一次刷新失败前照常显示，个别请求失败不会让列表闪烁。
  const offline = (!reachable && (!agents.data || isUnreachable(agents.error))) || (!scope && isUnreachable(listError))
  const loading = scope ? agents.isPending : !offline

  return (
    <section aria-labelledby={headingId} className="flex flex-col gap-[3px]">
      <div className={navGroupClass}>
        <h2 id={headingId} className="font-normal">
          {title}
        </h2>
        <button type="button" aria-label={t.agents.create} disabled={!reachable || !scope} onClick={onCreate} className={navAddClass}>
          <NavIcon name="plus" className="size-3.5 [stroke-width:3]" />
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
          {scope && <ul aria-labelledby={headingId} className="flex flex-col gap-[3px]">
            {shown.map((agent) => {
              const current = agent.id === currentAgentId
              return (
                <li key={agent.id} className="min-w-0">
                  <Link to={agentPath(scope, agent.id)} aria-current={current ? 'page' : undefined} className={navRowClass(current, true)}>
                    <AgentAvatar size="sm" id={agent.id} />
                    <span className="min-w-0 flex-1 truncate">{agent.name}</span>
                    <StatusTag agent={agent} />
                    <PendingBadge sticker counts={pendingByAgent.get(agent.id)} />
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
