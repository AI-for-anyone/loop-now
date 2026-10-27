import type { ReactNode } from 'react'
import type { Schemas } from '@/api/client'
import { pluginText as text } from '@/i18n/plugins'
import { cn } from '@/lib/utils'

// 插件页面共用的小部件：来源标签、id 与版本、状态、卡片外框。

export const cardClass = 'rounded-panel border-2 border-ink bg-white'

export function IdVersion({ id, version }: { id: string; version: string }) {
  return <code className="rounded-[6px] bg-ink/[0.06] px-2 py-0.5 font-mono text-[12px] leading-[18px] font-bold text-ink">{text.idVersion(id, version)}</code>
}

// 官方（随平台发布）用粉色贴纸；其余来源没有经过审核，用黄底写明来源。
export function SourceBadge({ source }: { source: Pick<Schemas['PluginResolvedSource'], 'kind'> }) {
  if (source.kind === 'builtin')
    return (
      <span className="riso-tilt rounded-[6px] px-1.5 py-px text-[11.5px] leading-4 font-black text-white [--tilt-bg:var(--riso-pink)] [--tilt-shadow:2px_2px_0_var(--ink)] [--tilt:-4deg]">
        {text.sources.builtin}
      </span>
    )
  return <span className="rounded-[6px] bg-tint-yellow px-1.5 py-px text-[11.5px] leading-4 font-black text-ink">{text.unverified(text.sources[source.kind])}</span>
}

const statusTone: Record<Schemas['PluginStatus'], { dot: string; text: string }> = {
  active: { dot: 'bg-[var(--status-done)]', text: 'text-green-text' },
  installed: { dot: 'bg-ink-500', text: 'text-ink-700' },
  error: { dot: 'bg-[var(--status-fail)]', text: 'text-red-text' },
  host_stopped: { dot: 'bg-[var(--status-interrupt)]', text: 'text-ink' },
}

export function StatusLabel({ status, className }: { status: Schemas['PluginStatus']; className?: string }) {
  const tone = statusTone[status]
  return (
    <span className={cn('inline-flex items-center gap-1.5 text-[13px] font-black whitespace-nowrap', tone.text, className)}>
      <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', tone.dot)} />
      {text.status[status]}
    </span>
  )
}

// 卡片里的小标题：展示字体，后面跟一行灰色说明。
export function PanelTitle({ title, hint, id, aside }: { title: string; hint?: ReactNode; id?: string; aside?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
      <h2 id={id} className="font-display-cn text-[22px] leading-tight font-normal">
        {title}
      </h2>
      {hint && <span className="text-[12px] font-bold text-ink-500">{hint}</span>}
      {aside && <div className="ml-auto">{aside}</div>}
    </div>
  )
}

export function Quiet({ children }: { children: ReactNode }) {
  return <p className="py-1.5 text-[13px] text-ink-500">{children}</p>
}
