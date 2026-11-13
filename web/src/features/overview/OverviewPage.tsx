import { useId, useState, useSyncExternalStore, type ReactNode } from 'react'
import { PlusIcon } from 'lucide-react'
import { reachability, type Schemas } from '@/api/client'
import type { ConnectionState } from '@/api/events'
import { Button } from '@/components/ui/button'
import { CreateAgentDialog } from '@/features/agents/CreateAgentDialog'
import { ScopeLabelContext, useOverviewFilter, type ScopeLabel } from '@/features/projects/overview-filter'
import { OverviewScopeEmpty, OverviewScopeNotFound, OverviewScopeSelect } from '@/features/projects/OverviewScope'
import { t } from '@/i18n'
import { lastScope, resolveLastScope, type ScopeQuery } from '@/lib/scope'
import { AnomaliesSection } from './AnomaliesSection'
import { PendingSection } from '@/features/console-inbox/PendingSection'
import { RunStatusSection } from './RunStatusSection'
import { UsageSection } from './UsageSection'
import { useOverviewLive } from './useOverviewLive'
import { LiveDot, PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet } from '@/components/Sheet'

// 没有任何 Agent 时：叠印插图、大标题和新建 Agent 的入口。
function EmptyOverview({ onCreate }: { onCreate: () => void }) {
  const titleId = useId()
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  return (
    <section aria-labelledby={titleId} className="flex flex-col items-center gap-[18px] px-6 py-14 text-center">
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

// 页头：页面标题，面包屑写着当前范围；标题旁的元信息是范围筛选与运行状态的连接状态（连上时“运行状态实时更新中”）。
function Header({ overview, connection }: { overview: Overview; connection?: ConnectionState }) {
  const { filter } = overview
  const scopeName = filter.kind === 'all' ? t.shell.global : filter.kind === 'platform' ? t.projects.overview.platform : filter.name
  return (
    <PageHeader crumbs={[{ label: scopeName }]} title={t.overview.title}>
      <OverviewScopeSelect filter={filter} projects={overview.projects} onSelect={overview.select} />
      {connection && <LiveDot state={connection} label={t.overview.live} />}
    </PageHeader>
  )
}

// 概览的内容排在一张纸上（改版）；纸张里的这一栏自己滚动。滚动区是定位容器：里面绝对定位的元素（如 sr-only 说明）
// 随内容滚动，不会按未滚动时的位置把整页撑高。
function OverviewSheet({ children }: { children: ReactNode }) {
  return (
    <Sheet className="mt-2 flex-1">
      <div className="relative min-h-0 flex-1 overflow-y-auto">
        <div className="flex max-w-[1260px] flex-col gap-6 pt-6 pr-[clamp(14px,3vw,40px)] pb-14 pl-[clamp(12px,2vw,30px)]">{children}</div>
      </div>
    </Sheet>
  )
}

// 概览页：打开控制台时的默认页，没有左侧 Agent 列表。纸张上是运行状态、用量、异常与失败三个编号分区（有发给你的
// 消息时，“待你处理”排在最上面，不编号），不放服务健康。默认包含所有未删除项目与平台中的 Agent；可以按项目或
// 平台筛选（写在地址中），换范围时内容区重新挂载并重新连接。
export function OverviewPage() {
  const overview = useOverviewFilter()
  const { filter } = overview
  return (
    <PageFrame>
      {overview.query ? (
        <OverviewContent key={overview.key} overview={overview} query={overview.query} />
      ) : (
        <>
          <Header overview={overview} />
          <OverviewSheet>
            {filter.kind === 'project' && filter.project === null && <OverviewScopeNotFound name={filter.name} onBack={() => overview.select('')} />}
          </OverviewSheet>
        </>
      )}
    </PageFrame>
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
      <Header overview={overview} connection={live.connection} />
      <OverviewSheet>
        {noAgents ? (
          filter.kind === 'all' ? (
            <EmptyOverview onCreate={() => setCreating(true)} />
          ) : (
            <OverviewScopeEmpty project={filter.kind === 'project' ? (filter.project ?? null) : null} />
          )
        ) : (
          <>
            <PendingSection scope={query} />
            <RunStatusSection live={live} number={1} />
            <UsageSection scope={query} number={2} />
            <AnomaliesSection scope={query} number={3} />
          </>
        )}
      </OverviewSheet>
      {creating && createScope && <CreateAgentDialog scope={createScope} onClose={() => setCreating(false)} />}
    </ScopeLabelContext.Provider>
  )
}
