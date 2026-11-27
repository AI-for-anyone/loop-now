import { useState } from 'react'
import type { Schemas } from '@/api/client'
import { usePluginDeliveries, usePluginLogs, usePluginState } from '@/api/plugins'
import { useProjects } from '@/api/queries'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { t } from '@/i18n'
import { selectClass } from '@/lib/field'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { Quiet } from './parts'
import { RECORD_PREFIX, recordRows, type DeliveryRecord } from './records'

type DeliveryStatus = Schemas['PluginDelivery']['status']
export type ActivityView = 'events' | 'records' | 'logs'

const rowClass = 'grid min-w-0 items-center gap-x-3 gap-y-0.5 rounded-input border border-hairline bg-surface-subtle px-3 py-2 text-[13px]'
const dot = (tone: 'done' | 'interrupt' | 'fail' | 'quiet') =>
  cn('size-2 shrink-0 rounded-full', tone === 'done' ? 'bg-[var(--status-done)]' : tone === 'interrupt' ? 'bg-[var(--status-interrupt)]' : tone === 'fail' ? 'bg-[var(--status-fail)]' : 'bg-ink-500')
const deliveryTone: Record<DeliveryStatus, 'done' | 'interrupt' | 'fail'> = { delivered: 'done', pending: 'interrupt', failed: 'fail' }
const toneText = { done: 'text-green-text', interrupt: 'text-ink', fail: 'text-red-text', quiet: 'text-ink-700' }

function More({ query, label }: { query: { hasNextPage: boolean; isFetchingNextPage: boolean; fetchNextPage: () => unknown }; label: string }) {
  if (!query.hasNextPage) return null
  return (
    <Button size="xs" variant="secondary" className="self-start" disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>
      {query.isFetchingNextPage ? t.common.loading : label}
    </Button>
  )
}

// Deliveries of observation events to the plugin: time, event, status (with handling time on success), attempt count and failure reason.
// compact is used in the project accordion: only the first few, no paging, no filtering. projectId shows only that project's events.
// When status is controlled by a parent toolbar, pass status + hideStatusFilter.
export function DeliveriesList({
  pluginId,
  compact,
  projectId,
  status: controlledStatus,
  hideStatusFilter = false,
}: {
  pluginId: string
  compact?: number
  projectId?: string
  status?: DeliveryStatus | 'all'
  hideStatusFilter?: boolean
}) {
  const [localStatus, setLocalStatus] = useState<DeliveryStatus | 'all'>('all')
  const status = controlledStatus ?? localStatus
  const text = t.plugins.events
  const query = usePluginDeliveries(pluginId, { ...(status === 'all' ? {} : { status }), ...(projectId ? { projectId } : {}) }, compact ?? 50)
  const rows = (query.data?.pages ?? []).flatMap((page) => page.deliveries).slice(0, compact ?? Infinity)
  return (
    <div className="flex min-w-0 flex-col gap-2">
      {!compact && !hideStatusFilter && (
        <ToggleGroup
          aria-label={text.filter}
          value={status}
          onValueChange={(value) => value && setLocalStatus(value as DeliveryStatus | 'all')}
          className={cn(segmentedClass, 'self-start')}
        >
          {(['all', 'pending', 'delivered', 'failed'] as const).map((value) => (
            <ToggleGroupItem key={value} value={value} className={segmentedItemClass}>
              {value === 'all' ? text.all : text.status[value]}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      )}
      <ErrorAlert error={query.error} />
      {query.isPending ? (
        <Quiet>{t.common.loading}</Quiet>
      ) : rows.length === 0 ? (
        <Quiet>{text.empty}</Quiet>
      ) : (
        <ul aria-label={t.plugins.detail.tabs.events} className="flex min-w-0 flex-col gap-1.5">
          {rows.map((row) => {
            const tone = deliveryTone[row.status]
            return (
              <li key={row.eventId} className={cn(rowClass, 'grid-cols-[auto_minmax(0,1fr)_auto]')}>
                <time dateTime={row.at} className="text-ink-700 tabular-nums">
                  {t.chat.time(row.at)}
                </time>
                <span className="truncate font-mono text-[12.5px] font-medium" title={row.eventType}>
                  {row.eventType}
                </span>
                <span className={cn('inline-flex items-center gap-1.5 justify-self-end font-semibold whitespace-nowrap', toneText[tone])}>
                  <span aria-hidden="true" className={dot(tone)} />
                  {[text.status[row.status], row.durationMs !== null && row.status === 'delivered' ? text.duration(row.durationMs) : null, row.attempts > 1 ? text.attempts(row.attempts) : null].filter(Boolean).join(' · ')}
                </span>
                {row.lastError && <span className="col-span-3 text-[12.5px] leading-snug text-red-text [overflow-wrap:anywhere]">{row.lastError}</span>}
              </li>
            )
          })}
        </ul>
      )}
      {!compact && <More query={query} label={text.more} />}
    </div>
  )
}

function recordState(record: DeliveryRecord): { label: string; tone: 'done' | 'interrupt' | 'fail' | 'quiet' } {
  const text = t.plugins.records
  const withCode = (label: string) => (record.lastStatusCode !== null ? text.code(label, record.lastStatusCode) : label)
  switch (record.status) {
    case 'delivered':
      return { label: record.lastStatusCode !== null ? String(record.lastStatusCode) : text.state.delivered, tone: 'done' }
    case 'failed':
      return record.nextAttemptAt ? { label: text.state.retrying, tone: 'interrupt' } : { label: withCode(text.state.failed), tone: 'fail' }
    case 'abandoned':
      return { label: withCode(text.state.abandoned), tone: 'fail' }
    default:
      return { label: text.state.pending, tone: 'quiet' }
  }
}

// Deliveries: the outbound records the plugin keeps under deliveries/* via the SDK, newest first by at; unrecognized records are shown as
// raw JSON.
export function RecordsTable({ pluginId, compact }: { pluginId: string; compact?: number }) {
  const text = t.plugins.records
  const query = usePluginState(pluginId, RECORD_PREFIX)
  const rows = recordRows(query.data?.entries ?? []).slice(0, compact ?? Infinity)
  if (query.error) return <ErrorAlert error={query.error} />
  if (query.isPending) return <Quiet>{t.common.loading}</Quiet>
  if (!rows.length) return <Quiet>{text.empty}</Quiet>
  return (
    <div className="min-w-0 overflow-x-auto no-scrollbar">
      <table className="w-full border-separate border-spacing-y-1.5 text-[13px]">
        <thead>
          <tr className="text-left text-[12px] font-semibold whitespace-nowrap text-ink-700">
            <th scope="col" className="px-3 font-semibold">{text.time}</th>
            <th scope="col" className="px-3 font-semibold">{text.target}</th>
            <th scope="col" className="px-3 font-semibold">{text.status}</th>
            <th scope="col" className="px-3 font-semibold">{text.attempts}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            if (!('record' in row))
              return (
                <tr key={row.key} className="bg-surface-subtle">
                  <td colSpan={4} className="rounded-input px-3 py-2">
                    <span className="font-mono text-[12px] font-medium">{row.key}</span>
                    <pre className="mt-1 max-h-32 overflow-auto no-scrollbar font-mono text-[11.5px] whitespace-pre-wrap [overflow-wrap:anywhere]" aria-label={text.raw}>
                      {JSON.stringify(row.raw, null, 2)}
                    </pre>
                  </td>
                </tr>
              )
            const { record } = row, state = recordState(record)
            return (
              <tr key={row.key} className="bg-surface-subtle">
                <td className="rounded-l-input px-3 py-2 whitespace-nowrap text-ink-700 tabular-nums">{t.chat.time(record.at)}</td>
                <td className="w-full max-w-0 truncate px-3 py-2 font-mono text-[12.5px]" title={record.target}>
                  {record.target}
                </td>
                <td className={cn('px-3 py-2 font-semibold whitespace-nowrap', toneText[state.tone])} title={record.lastError ?? undefined}>
                  <span className="inline-flex items-center gap-1.5">
                    <span aria-hidden="true" className={dot(state.tone)} />
                    {state.label}
                  </span>
                  {record.nextAttemptAt && record.status === 'failed' && <span className="block text-[11.5px] font-medium text-ink-500">{text.nextAttempt(t.chat.time(record.nextAttemptAt))}</span>}
                </td>
                <td className="rounded-r-input px-3 py-2 tabular-nums">{record.attempts}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

const levelTone: Record<Schemas['PluginLogEntry']['level'], string> = {
  debug: 'text-code-muted',
  info: 'text-code-fg',
  warn: 'text-pink-text',
  error: 'text-red-text',
}

// The plugin's host logs: newest first, kept for 7 days; earlier ones can be loaded further down.
export function LogsView({ pluginId, compact }: { pluginId: string; compact?: number }) {
  const text = t.plugins.logs
  const query = usePluginLogs(pluginId, compact ?? 100)
  const entries = (query.data?.pages ?? []).flatMap((page) => page.entries).slice(0, compact ?? Infinity)
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <ErrorAlert error={query.error} />
      {query.isPending ? (
        <Quiet>{t.common.loading}</Quiet>
      ) : entries.length === 0 ? (
        <Quiet>{text.empty}</Quiet>
      ) : (
        <ol aria-label={t.plugins.detail.logs} className="min-w-0 overflow-x-auto no-scrollbar rounded-input border border-code-border bg-code-bg px-3.5 py-3 font-mono text-[12px] leading-[1.7] text-code-fg">
          {entries.map((entry) => (
            <li key={entry.id} className="whitespace-pre-wrap [overflow-wrap:anywhere]">
              <time dateTime={entry.at} className="text-code-muted">
                {text.clock(entry.at)}
              </time>{' '}
              <span className={cn('font-bold', levelTone[entry.level])}>{entry.level}</span> {entry.line}
            </li>
          ))}
        </ol>
      )}
      {!compact && <More query={query} label={text.more} />}
    </div>
  )
}

// Unified Activity & Logs troubleshooting surface: inline segmented toolbar (Events | Outbound deliveries* | Host logs) plus inline status
// and project filters for Events, without a redundant outer Panel card.
export function ActivityPanel({
  pluginId,
  hasPlatform,
  initialView = 'events',
}: {
  pluginId: string
  hasPlatform: boolean
  initialView?: ActivityView
}) {
  const [view, setView] = useState<ActivityView>(initialView)
  const [prevInitial, setPrevInitial] = useState<ActivityView>(initialView)
  if (prevInitial !== initialView) {
    setPrevInitial(initialView)
    setView(initialView)
  }
  const [status, setStatus] = useState<DeliveryStatus | 'all'>('all')
  const [projectId, setProjectId] = useState<string>('')
  const projects = useProjects()
  const projectList = projects.data?.projects ?? []
  const showRecords = hasPlatform || view === 'records'
  const views: { key: ActivityView; label: string }[] = [
    { key: 'events', label: t.plugins.detail.tabs.events },
    ...(showRecords ? [{ key: 'records' as const, label: t.plugins.detail.outboundRecords }] : []),
    { key: 'logs', label: t.plugins.detail.hostLogs },
  ]

  return (
    <div className="flex min-w-0 flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2.5">
        <ToggleGroup
          aria-label={t.plugins.detail.activityView}
          value={view}
          onValueChange={(next) => next && setView(next as ActivityView)}
          className={cn(segmentedClass, 'self-start')}
        >
          {views.map((item) => (
            <ToggleGroupItem key={item.key} value={item.key} className={segmentedItemClass}>
              {item.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>

        {view === 'events' && (
          <div className="flex flex-wrap items-center gap-2 sm:ml-auto">
            <ToggleGroup
              aria-label={t.plugins.events.filter}
              value={status}
              onValueChange={(value) => value && setStatus(value as DeliveryStatus | 'all')}
              className={cn(segmentedClass, 'self-start')}
            >
              {(['all', 'pending', 'delivered', 'failed'] as const).map((value) => (
                <ToggleGroupItem key={value} value={value} className={segmentedItemClass}>
                  {value === 'all' ? t.plugins.events.all : t.plugins.events.status[value]}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
            <select
              aria-label={t.plugins.detail.projectFilter}
              value={projectId}
              onChange={(event) => setProjectId(event.target.value)}
              className={cn(selectClass, 'h-8 w-auto min-w-[140px] px-2.5 pr-8 text-[12.5px]')}
            >
              <option value="">{t.plugins.detail.allProjects}</option>
              {projectList.map((project) => (
                <option key={project.id} value={project.id}>
                  {project.name}
                </option>
              ))}
            </select>
          </div>
        )}
      </div>

      {view === 'events' && (
        <DeliveriesList
          pluginId={pluginId}
          status={status}
          projectId={projectId || undefined}
          hideStatusFilter
        />
      )}
      {view === 'records' && (
        <div className="flex min-w-0 flex-col gap-2">
          <p className="text-[12px] text-ink-500">{t.plugins.detail.recordsHint}</p>
          <RecordsTable pluginId={pluginId} />
        </div>
      )}
      {view === 'logs' && (
        <div className="flex min-w-0 flex-col gap-2">
          <p className="text-[12px] text-ink-500">{t.plugins.detail.logsHint}</p>
          <LogsView pluginId={pluginId} />
        </div>
      )}
    </div>
  )
}
