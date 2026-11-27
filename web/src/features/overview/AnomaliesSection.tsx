import { Link } from 'react-router'
import { ArrowRightIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { useAnomalies } from '@/api/queries'
import { ScopeTag } from '@/features/projects/OverviewScope'
import { dayLabel } from './day-label'
import type { ScopeQuery } from '@/lib/scope'
import { SectionError } from '@/components/RequestError'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { config } from '@/config'
import { t } from '@/i18n'
import { runErrorText } from '@/features/chat/run-status-text'
import { useRuntimeLookup } from '@/lib/runtime'

type Anomaly = Schemas['Anomaly']

const STAMP_TONE: Record<Schemas['AnomalyKind'], 'danger' | 'interrupted' | 'restart'> = {
  run_failed: 'danger',
  run_interrupted: 'interrupted',
  server_restart: 'restart',
}

// The matching field: the error code for failures, the end reason for interruptions and restart cut-offs.
function fieldOf(item: Anomaly): string {
  if (item.kind === 'run_failed') return t.timeline.result.errorField(item.errorCode ?? 'unknown', null)
  return t.timeline.result.endReasonField(item.endReason ?? (item.kind === 'server_restart' ? 'server_restart' : 'user_interrupt'))
}

// For a failed manual compaction (compaction is not null), append the command of that compaction.
const compactCommand = (compaction: Schemas['RunCompaction']) => (compaction.instructions ? `/compact ${compaction.instructions}` : '/compact')

// runtimeName: the name of the runtime the turn was on (Anomaly.runtime), or null when the anomaly doesn't say.
function reasonOf(item: Anomaly, runtimeName: string | null): string {
  if (item.kind === 'run_failed') return t.overview.anomalies.reason(runErrorText(item.errorCode, runtimeName), item.errorMessage)
  return item.kind === 'server_restart' ? t.overview.anomalies.restartReason : t.overview.anomalies.interruptedReason
}

// Each item jumps to that reply (or that manual compaction) and expands its run log.
function targetOf(item: Anomaly): { to: string; action: string } {
  return { to: `/agents/${item.agentId}/chat#run-${item.runId}`, action: item.compaction ? t.overview.anomalies.viewCompaction : t.overview.anomalies.view }
}

function AnomalyItem({ item }: { item: Anomaly }) {
  const runtimes = useRuntimeLookup()
  const { to, action } = targetOf(item)
  const field = fieldOf(item)
  return (
    <li>
      <Link
        to={to}
        className="grid grid-cols-[minmax(56px,80px)_minmax(0,1fr)] items-center gap-x-3.5 gap-y-1 border-b border-hairline px-1 py-2 text-ink hover:bg-surface-hover sm:grid-cols-[80px_minmax(0,1fr)_max-content]"
      >
        <span className="flex flex-col">
          <span className="font-mono text-[12.5px] leading-tight font-medium tabular-nums">
            {t.overview.anomalies.time(item.at)}
          </span>
          <span className="text-[11px] leading-tight text-ink-500">{dayLabel(item.at)}</span>
        </span>
        <span className="flex min-w-0 flex-col gap-0.5">
          <span className="flex flex-wrap items-center gap-x-2 gap-y-0.5">
            <Badge variant="stamp" tone={STAMP_TONE[item.kind]} className="[--stamp-tilt:0deg]">
              {item.compaction ? t.overview.anomalies.compactionFailed : t.overview.anomalies.kind[item.kind]}
            </Badge>
            <span className="text-[13.5px] font-semibold [overflow-wrap:anywhere]">{item.agentName}</span>
            <ScopeTag scope={item.scope} projectId={item.projectId} />
            <span
              title={field}
              className="max-w-full truncate rounded-stamp border border-hairline bg-surface-subtle px-1.5 py-0 font-mono text-[10.5px] leading-[18px] text-ink-500"
            >
              {field}
            </span>
          </span>
          <span className="text-[12.5px] leading-snug [overflow-wrap:anywhere] text-ink-700">
            {item.compaction && (
              <code className="mr-1.5 font-mono text-[12px] font-semibold text-ink">
                {compactCommand(item.compaction)}
              </code>
            )}
            {reasonOf(item, item.runtime ? runtimes.name(item.runtime) : null)}
          </span>
        </span>
        <span className="col-start-2 inline-flex items-center gap-1 text-[12.5px] font-semibold whitespace-nowrap sm:col-start-auto">
          {action}
          <ArrowRightIcon aria-hidden="true" className="size-3.5" strokeWidth={2.4} />
        </span>
      </Link>
    </li>
  )
}

// Issues and failures: replies in the last 24 hours that didn't complete normally, newest first, with "load more".
export function AnomaliesSection({
  scope = {},
  query,
}: {
  scope?: ScopeQuery
  query?: ReturnType<typeof useAnomalies>
}) {
  if (query) return <AnomaliesBody anomalies={query} />
  return <AnomaliesLoader scope={scope} />
}

function AnomaliesLoader({ scope }: { scope: ScopeQuery }) {
  const anomalies = useAnomalies(() => new Date(Date.now() - config.overviewDefaults.anomalyWindowHours * 60 * 60 * 1000).toISOString(), scope)
  return <AnomaliesBody anomalies={anomalies} />
}

function AnomaliesBody({ anomalies }: { anomalies: ReturnType<typeof useAnomalies> }) {
  const pages = anomalies.data?.pages
  const items = pages?.flatMap((page) => page.items) ?? []

  return (
    <section aria-label={t.overview.anomalies.title} className="flex min-w-0 flex-col gap-2.5">
      {pages && (
        <div className="flex items-center justify-between">
          <span className="text-[12px] text-ink-500">{t.overview.anomalies.count(pages[0].total)}</span>
        </div>
      )}
      {anomalies.isError && !anomalies.isFetchNextPageError && (
        <SectionError title={t.overview.anomalies.title} error={anomalies.error} onRetry={() => void anomalies.refetch()} />
      )}
      {anomalies.isPending ? (
        <div aria-busy="true" className="flex flex-col gap-2">
          <span className="riso-skeleton h-12 rounded-card" />
          <span className="riso-skeleton h-12 rounded-card opacity-70" />
          <span role="status" className="text-[12.5px] font-medium text-ink-500">
            {t.common.loading}
          </span>
        </div>
      ) : (
        pages &&
        (items.length === 0 ? (
          <p className="rounded-card border border-green/20 bg-tint-green px-4 py-3 text-[13px] font-medium text-green-text">
            {t.overview.anomalies.empty}
          </p>
        ) : (
          <ul className="flex flex-col border-t border-hairline">
            {items.map((item) => (
              <AnomalyItem key={item.runId} item={item} />
            ))}
          </ul>
        ))
      )}
      {anomalies.isFetchNextPageError && (
        <SectionError title={t.overview.anomalies.title} error={anomalies.error} onRetry={() => void anomalies.fetchNextPage()} />
      )}
      {anomalies.hasNextPage && (
        <Button variant="secondary" size="xs" className="self-center" disabled={anomalies.isFetchingNextPage} onClick={() => void anomalies.fetchNextPage()}>
          {anomalies.isFetchingNextPage ? t.common.loading : t.overview.anomalies.loadMore}
        </Button>
      )}
    </section>
  )
}
