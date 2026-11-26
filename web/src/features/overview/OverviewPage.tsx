import { useId, useState, useSyncExternalStore } from 'react'
import { Link, Navigate } from 'react-router'
import { DownloadIcon, PlusIcon } from 'lucide-react'
import { reachability, type Schemas } from '@/api/client'
import type { ConnectionState } from '@/api/events'
import { useAnomalies, useUsage } from '@/api/queries'
import { SubNav } from '@/components/IndexTabs'
import { LiveDot, PageFrame, PageHeader } from '@/components/PageHeader'
import { PageBody, Sheet } from '@/components/Sheet'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { config } from '@/config'
import { CreateAgentDialog } from '@/features/agents/CreateAgentDialog'
import { PendingSection } from '@/features/console-inbox/PendingSection'
import { firstRunImportShown, firstRunImportSkipped } from '@/features/importing/first-run'
import { ScopeLabelContext, useOverviewFilter, type OverviewFilter, type ScopeLabel } from '@/features/projects/overview-filter'
import { OverviewScopeEmpty, OverviewScopeNotFound } from '@/features/projects/OverviewScope'
import { t } from '@/i18n'
import { lastScope, PLATFORM, resolveLastScope, type ScopeQuery } from '@/lib/scope'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { AnomaliesSection } from './AnomaliesSection'
import { OverviewKpiStrip, RunStatusSection } from './RunStatusSection'
import { sumUsage, usageRangeStart } from './usage'
import { UsageSection } from './UsageSection'
import { useOverviewLive } from './useOverviewLive'

type OverviewTab = 'agents' | 'issues' | 'usage'

// When there are no Agents at all: the overprint illustration, a large title and a way to create an Agent.
function EmptyOverview({ onCreate }: { onCreate: () => void }) {
  const titleId = useId()
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  return (
    <section aria-labelledby={titleId} className="flex flex-col items-center gap-[18px] px-6 py-14 text-center">
      <span aria-hidden="true" className="relative hidden h-[140px] w-[200px] shrink-0 [[data-skin=plate]_&]:block">
        <span className="riso-halftone absolute top-2.5 left-2.5 size-[120px] rounded-full bg-surface-active [[data-skin=plate]_&]:bg-riso-blue" />
        <span className="absolute top-0 left-[70px] size-[120px] rounded-full border border-hairline-strong bg-surface-subtle [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-riso-pink [[data-skin=plate]_&]:mix-blend-multiply" />
      </span>
      <h2
        id={titleId}
        className="text-[22px] leading-snug font-semibold tracking-tight text-ink [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[40px] [[data-skin=plate]_&]:leading-[1.2] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal [[data-skin=plate]_&]:text-riso-blue"
      >
        {t.overview.empty.title}
      </h2>
      <p className="text-[14.5px] text-ink-700">{t.overview.empty.body}</p>
      <div className="flex flex-wrap items-center justify-center gap-3">
        <Button size="lg" className="h-12 px-[26px] text-base [[data-skin=plate]_&]:shadow-print-pink" disabled={!reachable} onClick={onCreate}>
          <PlusIcon strokeWidth={3} />
          {t.agents.create}
        </Button>
        <Button size="lg" variant="secondary" className="h-12 px-[26px] text-base" asChild>
          <Link to="/import">{t.importing.title}</Link>
        </Button>
      </div>
    </section>
  )
}

// The ownership tag on each row when All is selected: Platform for the platform, the name from the project list for a project,
// and Deleted project for one not in the list.
function labelsFor(projects: Schemas['Project'][]) {
  return (scope: Schemas['AgentScope'], projectId: string | null): ScopeLabel => {
    if (scope === 'platform') return { kind: 'platform' }
    const project = projects.find((p) => p.id === projectId)
    return project ? { kind: 'project', name: project.name } : { kind: 'deleted' }
  }
}

function statsPathFor(filter: OverviewFilter): string {
  if (filter.kind === 'platform') return '/stats?platform=1'
  if (filter.kind === 'project') return `/stats?${new URLSearchParams({ project: filter.name })}`
  return '/stats'
}

type Overview = ReturnType<typeof useOverviewFilter>

// Header: single-row bar with current scope breadcrumb, compact All | Platform segmented toggle when in Global scope (inside a
// project the Sidebar ScopeSwitcher already owns scope selection), live status dot, and compact Import / + New Agent actions.
function Header({
  overview,
  connection,
  onCreate,
  canCreate,
  showActions,
}: {
  overview: Overview
  connection?: ConnectionState
  onCreate?: () => void
  canCreate?: boolean
  showActions?: boolean
}) {
  const { filter } = overview
  const reachable = useSyncExternalStore(reachability.subscribe, reachability.isReachable)
  const scopeName = filter.kind === 'all' ? t.shell.global : filter.kind === 'platform' ? t.projects.overview.platform : filter.name
  const inGlobal = filter.kind === 'all' || filter.kind === 'platform'

  return (
    <PageHeader
      crumbs={[{ label: scopeName }]}
      title={t.overview.title}
      actions={
        showActions ? (
          <>
            <Button variant="secondary" size="xs" className="h-8 px-3 text-[12.5px]" asChild>
              <Link to="/import">
                <DownloadIcon aria-hidden="true" className="size-3.5" strokeWidth={2.2} />
                {t.overview.actions.import}
              </Link>
            </Button>
            {onCreate && canCreate && (
              <Button size="xs" className="h-8 px-3 text-[12.5px]" disabled={!reachable} onClick={onCreate}>
                <PlusIcon aria-hidden="true" className="size-3.5" strokeWidth={2.5} />
                {t.agents.create}
              </Button>
            )}
          </>
        ) : undefined
      }
    >
      {inGlobal && (
        <ToggleGroup
          aria-label={t.projects.overview.range}
          className={cn(segmentedClass, 'h-7')}
          value={filter.kind === 'platform' ? PLATFORM : 'all'}
          onValueChange={(value) => {
            if (!value) return
            overview.select(value === PLATFORM ? PLATFORM : '')
          }}
        >
          <ToggleGroupItem value="all" className={cn(segmentedItemClass, 'px-2.5 text-[12px]')}>
            {t.projects.overview.all}
          </ToggleGroupItem>
          <ToggleGroupItem value={PLATFORM} className={cn(segmentedItemClass, 'px-2.5 text-[12px]')}>
            {t.projects.overview.platform}
          </ToggleGroupItem>
        </ToggleGroup>
      )}
      {connection && <LiveDot state={connection} label={t.overview.live} />}
    </PageHeader>
  )
}

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
          <Sheet className="flex-1 [[data-skin=plate]_&]:mt-2">
            <PageBody width="wide">
              {filter.kind === 'project' && filter.project === null && (
                <OverviewScopeNotFound name={filter.name} onBack={() => overview.select('')} />
              )}
            </PageBody>
          </Sheet>
        </>
      )}
    </PageFrame>
  )
}

function OverviewContent({ overview, query }: { overview: Overview; query: ScopeQuery }) {
  const live = useOverviewLive(query)
  const [tab, setTab] = useState<OverviewTab>('agents')
  const [creating, setCreating] = useState(false)
  const remembered = useSyncExternalStore(lastScope.subscribe, lastScope.get)
  const { filter, projects } = overview
  const noAgents = live.synced && live.agents.length === 0
  const createScope =
    filter.kind === 'project' && filter.project
      ? ({ kind: 'project', project: filter.project } as const)
      : filter.kind === 'platform'
        ? ({ kind: 'platform' } as const)
        : projects
          ? resolveLastScope(projects, remembered)
          : null
  const labels = filter.kind === 'all' && projects ? labelsFor(projects) : null
  const statsHref = statsPathFor(filter)

  // Shared query keys with UsageSection and AnomaliesSection: populates the KPI strip and tab counts without extra requests.
  const todayUsage = useUsage('today', () => usageRangeStart('today').toISOString(), query)
  const anomalies = useAnomalies(() => new Date(Date.now() - config.overviewDefaults.anomalyWindowHours * 60 * 60 * 1000).toISOString(), query)
  const todayTokens = todayUsage.data ? sumUsage(todayUsage.data.rows).total : undefined
  const issuesCount = anomalies.data?.pages[0]?.total

  return (
    <ScopeLabelContext.Provider value={labels}>
      <Header
        overview={overview}
        connection={live.connection}
        showActions={!noAgents}
        canCreate={createScope !== null}
        onCreate={() => setCreating(true)}
      />
      <Sheet className="flex-1 [[data-skin=plate]_&]:mt-2">
        <PageBody width="wide" innerClassName="flex flex-col gap-4">
          {noAgents ? (
            filter.kind === 'all' ? (
              firstRunImportSkipped() || firstRunImportShown() ? (
                <EmptyOverview onCreate={() => setCreating(true)} />
              ) : (
                <Navigate to="/import?first=1" replace />
              )
            ) : (
              <OverviewScopeEmpty project={filter.kind === 'project' ? (filter.project ?? null) : null} />
            )
          ) : (
            <>
              <PendingSection scope={query} />
              <OverviewKpiStrip
                live={live}
                issuesCount={issuesCount}
                todayTokens={todayTokens}
                statsHref={statsHref}
                onOpenIssues={() => setTab('issues')}
              />
              <SubNav
                aria-label={t.overview.tabs.label}
                value={tab}
                onChange={setTab}
                className="px-0 sm:px-0 [[data-skin=plate]_&]:pl-0"
                items={[
                  {
                    key: 'agents',
                    label: t.overview.tabs.agents,
                    count: live.synced ? live.agents.length : undefined,
                    controls: 'overview-panel-agents',
                  },
                  {
                    key: 'issues',
                    label: t.overview.tabs.issues,
                    count: issuesCount,
                    badge:
                      issuesCount !== undefined && issuesCount > 0 ? (
                        <span aria-hidden="true" className="size-1.5 rounded-full bg-red" />
                      ) : undefined,
                    controls: 'overview-panel-issues',
                  },
                  {
                    key: 'usage',
                    label: t.overview.tabs.usage,
                    controls: 'overview-panel-usage',
                  },
                ]}
              />
              <div id={`overview-panel-${tab}`} role="tabpanel" className="min-w-0">
                {tab === 'agents' && <RunStatusSection live={live} />}
                {tab === 'issues' && <AnomaliesSection scope={query} query={anomalies} />}
                {tab === 'usage' && <UsageSection scope={query} statsHref={statsHref} todayQuery={todayUsage} />}
              </div>
            </>
          )}
        </PageBody>
      </Sheet>
      {creating && createScope && <CreateAgentDialog scope={createScope} onClose={() => setCreating(false)} />}
    </ScopeLabelContext.Provider>
  )
}
