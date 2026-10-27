import { useId } from 'react'
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

type Anomaly = Schemas['Anomaly']

const STAMP_TONE: Record<Schemas['AnomalyKind'], 'danger' | 'interrupted' | 'restart'> = {
  run_failed: 'danger',
  run_interrupted: 'interrupted',
  server_restart: 'restart',
}


// 对应的字段：失败为错误码，打断与重启中断为结束原因。
function fieldOf(item: Anomaly): string {
  if (item.kind === 'run_failed') return t.timeline.result.errorField(item.errorCode ?? 'unknown', null)
  return t.timeline.result.endReasonField(item.endReason ?? (item.kind === 'server_restart' ? 'server_restart' : 'user_interrupt'))
}

function reasonOf(item: Anomaly): string {
  if (item.kind === 'run_failed') {
    const reason = item.errorCode ? (t.chat.runError[item.errorCode] ?? t.common.unknown(item.errorCode)) : t.errors.unknown
    return t.overview.anomalies.reason(reason, item.errorMessage)
  }
  return item.kind === 'server_restart' ? t.overview.anomalies.restartReason : t.overview.anomalies.interruptedReason
}

// 会话文件丢失进入对话页，由那里的横幅处理重开会话；其他条目定位到这条回复并展开它的运行记录。
function targetOf(item: Anomaly): { to: string; action: string } {
  if (item.errorCode === 'session_missing') return { to: `/agents/${item.agentId}/chat`, action: t.overview.anomalies.handle }
  return { to: `/agents/${item.agentId}/chat#run-${item.runId}`, action: t.overview.anomalies.view }
}

function AnomalyItem({ item }: { item: Anomaly }) {
  const { to, action } = targetOf(item)
  return (
    <li>
      <Link
        to={to}
        className="grid min-h-16 grid-cols-[minmax(64px,96px)_minmax(0,1fr)] items-center gap-x-4 gap-y-1.5 rounded-[16px] bg-white px-[18px] py-3 text-ink shadow-print-rest hover:bg-tint-pink-2 sm:grid-cols-[96px_minmax(0,1fr)_max-content]"
      >
        <span className="flex flex-col">
          <span className="font-mono text-[13px] font-bold">{t.overview.anomalies.time(item.at)}</span>
          <span className="text-[12px] text-ink-500">{dayLabel(item.at)}</span>
        </span>
        <span className="flex min-w-0 flex-col gap-[3px]">
          <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
            {/* 列表中的印章不倾斜，免得一屏中倾斜的元素过多。 */}
            <Badge variant="stamp" tone={STAMP_TONE[item.kind]} className="[--stamp-tilt:0deg]">
              {t.overview.anomalies.kind[item.kind]}
            </Badge>
            <span className="text-[14.5px] font-black [overflow-wrap:anywhere]">{item.agentName}</span>
            <ScopeTag scope={item.scope} projectId={item.projectId} />
            <span className="font-mono text-[11.5px] [overflow-wrap:anywhere] text-ink-500">{fieldOf(item)}</span>
          </span>
          <span className="text-[13.5px] [overflow-wrap:anywhere] text-ink-700">{reasonOf(item)}</span>
        </span>
        <span className="col-start-2 inline-flex items-center gap-1.5 text-[13.5px] font-black whitespace-nowrap sm:col-start-auto">
          {action}
          <ArrowRightIcon aria-hidden="true" className="size-3.5" strokeWidth={2.6} />
        </span>
      </Link>
    </li>
  )
}

// 异常与失败：最近 24 小时内未正常完成的回复，从新到旧，可以加载更多。
export function AnomaliesSection({ scope = {} }: { scope?: ScopeQuery }) {
  const titleId = useId()
  const anomalies = useAnomalies(() => new Date(Date.now() - config.overviewDefaults.anomalyWindowHours * 60 * 60 * 1000).toISOString(), scope)
  const pages = anomalies.data?.pages
  const items = pages?.flatMap((page) => page.items) ?? []

  return (
    <section aria-labelledby={titleId} className="flex flex-col gap-[18px]">
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-2.5">
        <h2 id={titleId} className="riso-misreg-sm font-display-cn text-[40px] leading-[1.1] font-normal">
          {t.overview.anomalies.title}
        </h2>
        {pages && <span className="text-[13px] font-bold text-ink-500">{t.overview.anomalies.count(pages[0].total)}</span>}
      </div>
      {anomalies.isError && !anomalies.isFetchNextPageError && (
        <SectionError title={t.overview.anomalies.title} error={anomalies.error} onRetry={() => void anomalies.refetch()} />
      )}
      {anomalies.isPending ? (
        <div aria-busy="true" className="flex flex-col gap-2.5">
          <span className="riso-skeleton h-16 rounded-[16px]" />
          <span className="riso-skeleton h-16 rounded-[16px] opacity-70" />
          <span role="status" className="text-[13px] font-bold text-ink-500">
            {t.common.loading}
          </span>
        </div>
      ) : (
        pages &&
        (items.length === 0 ? (
          <p className="rounded-[16px] bg-tint-green p-6 text-center text-[14.5px] font-bold text-green-text">{t.overview.anomalies.empty}</p>
        ) : (
          <ul className="flex flex-col gap-2.5">
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
        <Button variant="secondary" className="self-center" disabled={anomalies.isFetchingNextPage} onClick={() => void anomalies.fetchNextPage()}>
          {anomalies.isFetchingNextPage ? t.common.loading : t.overview.anomalies.loadMore}
        </Button>
      )}
    </section>
  )
}
