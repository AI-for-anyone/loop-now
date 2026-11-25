import { useId, useState, useSyncExternalStore, type ReactNode } from 'react'
import { Link, Navigate } from 'react-router'
import { firstRunImportSkipped } from '@/features/importing/first-run'
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

// When there are no Agents at all: the overprint illustration, a large title and a way to create an Agent.
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
      <div className="flex flex-wrap items-center justify-center gap-3">
        <Button size="lg" className="h-12 px-[26px] text-base shadow-print-pink" disabled={!reachable} onClick={onCreate}>
          <PlusIcon strokeWidth={3} />
          {t.agents.create}
        </Button>
        {/* Or bring the conversations from the terminal over. */}
        <Button size="lg" variant="secondary" className="h-12 px-[26px] text-base" asChild>
          <Link to="/import">{t.importing.title}</Link>
        </Button>
      </div>
    </section>
  )
}

// The ownership tag on each row when “全部” (All) is selected: “平台” (Platform) for the platform, the name from the project list
// for a project, and “已删除的项目” (Deleted project) for one not in the list (deleted).
function labelsFor(projects: Schemas['Project'][]) {
  return (scope: Schemas['AgentScope'], projectId: string | null): ScopeLabel => {
    if (scope === 'platform') return { kind: 'platform' }
    const project = projects.find((p) => p.id === projectId)
    return project ? { kind: 'project', name: project.name } : { kind: 'deleted' }
  }
}

type Overview = ReturnType<typeof useOverviewFilter>

// Header: the page title, with the current scope in the breadcrumb; the meta info next to the title is the scope filter and the
// connection state of the run status (“运行状态实时更新中” (Run status is updating live) when connected).
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

// The overview content is laid out on one sheet (redesign); this column inside the sheet scrolls on its own. The scroll area is
// the positioning container: absolutely positioned elements inside it (such as sr-only text) scroll with the content instead
// of stretching the whole page based on their unscrolled position.
function OverviewSheet({ children }: { children: ReactNode }) {
  return (
    <Sheet className="mt-2 flex-1">
      <div className="relative min-h-0 flex-1 overflow-y-auto">
        <div className="flex max-w-[1260px] flex-col gap-6 pt-6 pr-[clamp(14px,3vw,40px)] pb-14 pl-[clamp(12px,2vw,30px)]">{children}</div>
      </div>
    </Sheet>
  )
}

// The overview page: the default page when the console opens, without the sidebar Agent list. The sheet holds three numbered
// sections: run status, usage, and issues and failures (when there are messages for you, “待你处理” (Needs you) goes on top,
// unnumbered); service health is not shown. By default it includes the Agents in all undeleted projects and the platform; it can
// be filtered by project or platform (stored in the URL), and changing the scope remounts the content area and reconnects.
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
  // With no Agents at all, create in the most recently used project or the platform, or in the default project when nothing is
  // recorded, consistent with "Agent" in the top navigation.
  const createScope = projects ? resolveLastScope(projects, remembered) : null
  const labels = filter.kind === 'all' && projects ? labelsFor(projects) : null

  return (
    <ScopeLabelContext.Provider value={labels}>
      <Header overview={overview} connection={live.connection} />
      <OverviewSheet>
        {noAgents ? (
          filter.kind === 'all' ? (
            // A fresh install (no Agents anywhere) opens the import page once, as T3's welcome page; skipping it is remembered.
            firstRunImportSkipped() ? <EmptyOverview onCreate={() => setCreating(true)} /> : <Navigate to="/import?first=1" replace />
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
