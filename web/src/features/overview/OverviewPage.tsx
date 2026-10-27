import { useId, useState, useSyncExternalStore } from 'react'
import { PlusIcon } from 'lucide-react'
import { reachability, type Schemas } from '@/api/client'
import type { ConnectionState } from '@/api/events'
import { Button } from '@/components/ui/button'
import { CreateAgentDialog } from '@/features/agents/CreateAgentDialog'
import { ScopeLabelContext, useOverviewFilter, type ScopeLabel } from '@/features/projects/overview-filter'
import { OverviewScopeEmpty, OverviewScopeNotFound, OverviewScopeSelect } from '@/features/projects/OverviewScope'
import { t } from '@/i18n'
import { lastScope, resolveLastScope, type ScopeQuery } from '@/lib/scope'
import { cn } from '@/lib/utils'
import { AnomaliesSection } from './AnomaliesSection'
import { PendingSection } from '@/features/console-inbox/PendingSection'
import { RunStatusSection } from './RunStatusSection'
import { UsageSection } from './UsageSection'
import { useOverviewLive } from './useOverviewLive'
import { pageTitleClass } from '@/lib/page-title'

// 运行状态事件流的连接状态：正常时为绿色“实时更新中”，断开时为红色“正在重连”，沿用对话页的文案。
function LiveStatus({ connection }: { connection: ConnectionState }) {
  const state = connection === 'closed' ? 'reconnecting' : connection
  return (
    <span
      role="status"
      aria-live="polite"
      className={cn(
        'inline-flex items-center gap-2 rounded-full px-3.5 py-1 text-[13px] font-black',
        state === 'open' ? 'bg-tint-green text-green-text' : state === 'reconnecting' ? 'bg-tint-red text-red-text' : 'bg-tint-blue text-ink',
      )}
    >
      <span
        aria-hidden="true"
        className={cn('size-2 shrink-0 rounded-full', state === 'open' ? 'bg-green' : state === 'reconnecting' ? 'bg-red' : 'bg-ink-500')}
      />
      {state === 'open' ? t.overview.live : t.chat.connection[state]}
    </span>
  )
}

// 没有任何 Agent 时：叠印插图、大标题和新建 Agent 的入口。
function EmptyOverview({ onCreate }: { onCreate: () => void }) {
  const titleId = useId()
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  return (
    <section
      aria-labelledby={titleId}
      className="flex flex-col items-center gap-[18px] rounded-dialog bg-paper-raised px-6 py-14 text-center shadow-print-rest"
    >
      <span aria-hidden="true" className="relative h-[140px] w-[200px] shrink-0">
        <span className="riso-halftone absolute top-2.5 left-2.5 size-[120px] rounded-full bg-riso-blue" />
        <span className="absolute top-0 left-[70px] size-[120px] rounded-full bg-riso-pink mix-blend-multiply" />
      </span>
      <h2 id={titleId} className="font-display-cn text-[40px] leading-[1.2] font-normal text-riso-blue">
        {t.overview.empty.title}
      </h2>
      <p className="text-[14.5px] text-ink-700">{t.overview.empty.body}</p>
      <Button size="lg" className="h-12 px-[26px] text-base shadow-print-pink" disabled={!reachable} onClick={onCreate}>
        <PlusIcon strokeWidth={3} />
        {t.agents.create}
      </Button>
    </section>
  )
}

// 选“全部”时各行的所属标签：平台为“平台”，项目取项目列表中的名称；不在列表中（已删除）的为“已删除的项目”。
function labelsFor(projects: Schemas['Project'][]) {
  return (scope: Schemas['AgentScope'], projectId: string | null): ScopeLabel => {
    if (scope === 'platform') return { kind: 'platform' }
    const project = projects.find((p) => p.id === projectId)
    return project ? { kind: 'project', name: project.name } : { kind: 'deleted' }
  }
}

type Overview = ReturnType<typeof useOverviewFilter>

// 标题行：大标题，右侧是范围筛选与运行状态的连接状态。
function TitleRow({ overview, connection }: { overview: Overview; connection?: ConnectionState }) {
  return (
    <div className="flex flex-wrap items-end gap-x-6 gap-y-3">
      <h1 className={pageTitleClass}>{t.overview.title}</h1>
      <span className="mb-3 flex w-full flex-wrap items-center gap-x-4 gap-y-2.5 sm:ml-auto sm:w-auto">
        <OverviewScopeSelect filter={overview.filter} projects={overview.projects} onSelect={overview.select} />
        {connection && <LiveStatus connection={connection} />}
      </span>
    </div>
  )
}

// 概览页：打开控制台时的默认页，没有左侧 Agent 列表。内容区是运行状态、用量、异常与失败三块，不放服务健康。
// 默认包含所有未删除项目与平台中的 Agent；可以按项目或平台筛选（写在地址中），换范围时内容区重新挂载并重新连接。
export function OverviewPage() {
  const overview = useOverviewFilter()
  const { filter } = overview
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-[1260px] flex-col gap-[34px] px-4 pt-[34px] pb-[72px] sm:px-[30px]">
        {overview.query ? (
          <OverviewContent key={overview.key} overview={overview} query={overview.query} />
        ) : (
          <>
            <TitleRow overview={overview} />
            {filter.kind === 'project' && filter.project === null && (
              <OverviewScopeNotFound name={filter.name} onBack={() => overview.select('')} />
            )}
          </>
        )}
      </div>
    </div>
  )
}

function OverviewContent({ overview, query }: { overview: Overview; query: ScopeQuery }) {
  const live = useOverviewLive(query)
  const [creating, setCreating] = useState(false)
  const remembered = useSyncExternalStore(lastScope.subscribe, lastScope.get)
  const { filter, projects } = overview
  const noAgents = live.synced && live.agents.length === 0
  // 没有任何 Agent 时新建在最近使用的项目或平台中，没有记录时在默认项目中，与顶部导航的“Agent”一致。
  const createScope = projects ? resolveLastScope(projects, remembered) : null
  const labels = filter.kind === 'all' && projects ? labelsFor(projects) : null

  return (
    <ScopeLabelContext.Provider value={labels}>
      <TitleRow overview={overview} connection={live.connection} />
      {noAgents ? (
        filter.kind === 'all' ? (
          <EmptyOverview onCreate={() => setCreating(true)} />
        ) : (
          <OverviewScopeEmpty project={filter.kind === 'project' ? (filter.project ?? null) : null} />
        )
      ) : (
        <>
          <PendingSection scope={query} />
          <RunStatusSection live={live} />
          <UsageSection scope={query} />
          <AnomaliesSection scope={query} />
        </>
      )}
      {creating && createScope && <CreateAgentDialog scope={createScope} onClose={() => setCreating(false)} />}
    </ScopeLabelContext.Provider>
  )
}
