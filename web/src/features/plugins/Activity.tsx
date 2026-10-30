import { useState } from 'react'
import type { Schemas } from '@/api/client'
import { usePluginDeliveries, usePluginLogs, usePluginState } from '@/api/plugins'
import { ErrorAlert } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { Quiet } from './parts'
import { RECORD_PREFIX, recordRows, type DeliveryRecord } from './records'

type DeliveryStatus = Schemas['PluginDelivery']['status']

const rowClass = 'grid min-w-0 items-center gap-x-3 gap-y-0.5 rounded-input bg-ink/[0.04] px-3 py-2 text-[13px]'
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

// 观察类事件对插件的投递：时间、事件、状态（成功时带处理耗时）、尝试次数与失败原因。
// compact 用在概览里：只取前几条，不分页、不筛选。projectId 只看这个项目的事件。
export function DeliveriesList({ pluginId, compact, projectId }: { pluginId: string; compact?: number; projectId?: string }) {
  const [status, setStatus] = useState<DeliveryStatus | 'all'>('all')
  const text = t.plugins.events
  const query = usePluginDeliveries(pluginId, { ...(status === 'all' ? {} : { status }), ...(projectId ? { projectId } : {}) }, compact ?? 50)
  const rows = (query.data?.pages ?? []).flatMap((page) => page.deliveries).slice(0, compact ?? Infinity)
  return (
    <div className="flex min-w-0 flex-col gap-2">
      {!compact && (
        <ToggleGroup aria-label={text.filter} value={status} onValueChange={(value) => value && setStatus(value as DeliveryStatus | 'all')} className="self-start border-2 border-ink bg-white p-[3px]">
          {(['all', 'pending', 'delivered', 'failed'] as const).map((value) => (
            <ToggleGroupItem key={value} value={value} className="min-h-7 px-3 text-[13px] data-[state=on]:shadow-none">
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
        <ul className="flex min-w-0 flex-col gap-1.5">
          {rows.map((row) => {
            const tone = deliveryTone[row.status]
            return (
              <li key={row.eventId} className={cn(rowClass, 'grid-cols-[auto_minmax(0,1fr)_auto]')}>
                <time dateTime={row.at} className="text-ink-700 tabular-nums">
                  {t.chat.time(row.at)}
                </time>
                <span className="truncate font-mono text-[12.5px] font-bold" title={row.eventType}>
                  {row.eventType}
                </span>
                <span className={cn('inline-flex items-center gap-1.5 justify-self-end font-black whitespace-nowrap', toneText[tone])}>
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

// 投递记录：插件经 SDK 记在 deliveries/* 下的外发记录，按 at 新的在前；认不得的记录原样显示 JSON。
export function RecordsTable({ pluginId, compact }: { pluginId: string; compact?: number }) {
  const text = t.plugins.records
  const query = usePluginState(pluginId, RECORD_PREFIX)
  const rows = recordRows(query.data?.entries ?? []).slice(0, compact ?? Infinity)
  if (query.error) return <ErrorAlert error={query.error} />
  if (query.isPending) return <Quiet>{t.common.loading}</Quiet>
  if (!rows.length) return <Quiet>{text.empty}</Quiet>
  return (
    <div className="min-w-0 overflow-x-auto">
      <table className="w-full border-separate border-spacing-y-1.5 text-[13px]">
        <thead>
          <tr className="text-left text-[12px] font-black whitespace-nowrap text-ink-700">
            <th scope="col" className="px-3 font-black">{text.time}</th>
            <th scope="col" className="px-3 font-black">{text.target}</th>
            <th scope="col" className="px-3 font-black">{text.status}</th>
            <th scope="col" className="px-3 font-black">{text.attempts}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            if (!('record' in row))
              return (
                <tr key={row.key} className="bg-ink/[0.04]">
                  <td colSpan={4} className="rounded-input px-3 py-2">
                    <span className="font-mono text-[12px] font-bold">{row.key}</span>
                    <pre className="mt-1 max-h-32 overflow-auto font-mono text-[11.5px] whitespace-pre-wrap [overflow-wrap:anywhere]" aria-label={text.raw}>
                      {JSON.stringify(row.raw, null, 2)}
                    </pre>
                  </td>
                </tr>
              )
            const { record } = row, state = recordState(record)
            return (
              <tr key={row.key} className="bg-ink/[0.04]">
                <td className="rounded-l-input px-3 py-2 whitespace-nowrap text-ink-700 tabular-nums">{t.chat.time(record.at)}</td>
                <td className="w-full max-w-0 truncate px-3 py-2 font-mono text-[12.5px]" title={record.target}>
                  {record.target}
                </td>
                <td className={cn('px-3 py-2 font-black whitespace-nowrap', toneText[state.tone])} title={record.lastError ?? undefined}>
                  <span className="inline-flex items-center gap-1.5">
                    <span aria-hidden="true" className={dot(state.tone)} />
                    {state.label}
                  </span>
                  {record.nextAttemptAt && record.status === 'failed' && <span className="block text-[11.5px] font-bold text-ink-500">{text.nextAttempt(t.chat.time(record.nextAttemptAt))}</span>}
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

const levelTone: Record<Schemas['PluginLogEntry']['level'], string> = { debug: 'text-white/55', info: 'text-white/85', warn: 'text-riso-yellow', error: 'text-[#FF8A80]' }

// 插件的宿主日志：新的在前，保留 7 天；往下可以加载更早的。
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
        <ol aria-label={t.plugins.detail.logs} className="min-w-0 overflow-x-auto rounded-input bg-ink px-3.5 py-3 font-mono text-[12px] leading-[1.7] text-white">
          {entries.map((entry) => (
            <li key={entry.id} className="whitespace-pre-wrap [overflow-wrap:anywhere]">
              <time dateTime={entry.at} className="text-white/60">
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
