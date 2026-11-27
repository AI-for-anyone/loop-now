import type { ReactNode } from 'react'
import { ExternalLinkIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { pluginTrust, usePluginMarkets } from '@/api/plugins'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// Small parts shared by the plugin pages: source badge, id and version, status, card frame.

const text = t.plugins

export const cardClass = 'rounded-panel border border-hairline-strong bg-surface-control shadow-surface'

const BANDS = ['blue', 'pink', 'yellow'] as const
export const bandCardClass = 'flex min-w-0 flex-col overflow-hidden rounded-card border border-hairline-strong bg-surface-control shadow-surface'
export function CardBand({ index }: { index: number }) {
  const band = BANDS[index % BANDS.length]
  return <span aria-hidden="true" data-band={band} className="hidden" />
}
export const cardButtonClass = ''

export function IdVersion({ id, version }: { id: string; version: string }) {
  return <code className="rounded-stamp border border-hairline bg-surface-subtle px-2 py-0.5 font-mono text-[12px] leading-[18px] font-medium text-ink">{text.idVersion(id, version)}</code>
}

// Official plugins get a primary pill; verified market entries get a green pill; unreviewed sources get a yellow pill.
export function SourceBadge({ source, trust = source.kind === 'builtin' ? 'official' : null }: { source: Pick<Schemas['PluginResolvedSource'], 'kind'>; trust?: 'official' | 'verified' | null }) {
  if (trust === 'official')
    return <span data-source-badge className="rounded-full bg-cta-bg px-2 text-[11.5px] leading-5 font-semibold text-cta-fg">{text.sources.builtin}</span>
  if (trust === 'verified') return <span data-source-badge className="rounded-stamp bg-tint-green px-1.5 py-px text-[11.5px] leading-4 font-semibold text-green-text">{text.verifiedSource(text.sources[source.kind])}</span>
  return <span data-source-badge className="rounded-stamp bg-tint-yellow px-1.5 py-px text-[11.5px] leading-4 font-semibold text-ink">{text.unverified(text.sources[source.kind])}</span>
}

// Source badge of an installed plugin: one installed from a market is judged by the market (the official market and verified entries
// count as reviewed), and nothing is shown until the market list is read; if reading fails, it is treated as unreviewed.
export function InstalledSourceBadge({ plugin }: { plugin: Pick<Schemas['Plugin'], 'id' | 'source'> }) {
  const markets = usePluginMarkets(plugin.source.market !== null)
  const trust = pluginTrust(markets.isError ? [] : markets.data?.markets, plugin)
  if (trust === undefined) return null
  return <SourceBadge source={plugin.source} trust={trust} />
}

const statusTone: Record<Schemas['PluginStatus'], { dot: string; text: string }> = {
  active: { dot: 'bg-[var(--status-done)]', text: 'text-green-text' },
  installed: { dot: 'bg-ink-500', text: 'text-ink-700' },
  error: { dot: 'bg-[var(--status-fail)]', text: 'text-red-text' },
  host_stopped: { dot: 'bg-[var(--status-interrupt)]', text: 'text-ink' },
}

export const surfaceListClass =
  'divide-y divide-hairline overflow-hidden rounded-card border border-hairline-strong bg-surface-control shadow-surface'

export function StatusDot({ status, muted = false, className }: { status: Schemas['PluginStatus']; muted?: boolean; className?: string }) {
  return <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', muted ? 'bg-ink-500/40' : statusTone[status].dot, className)} />
}

export function StatusLabel({ status, className }: { status: Schemas['PluginStatus']; className?: string }) {
  const tone = statusTone[status]
  return (
    <span className={cn('inline-flex items-center gap-1.5 text-[13px] font-medium whitespace-nowrap', tone.text, className)}>
      <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', tone.dot)} />
      {text.status[status]}
    </span>
  )
}

export function PanelTitle({ title, hint, id, aside }: { title: string; hint?: ReactNode; id?: string; aside?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
      <h2 id={id} className="font-display-cn text-[17px] leading-snug font-semibold tracking-tight">
        {title}
      </h2>
      {hint && <span className="text-[12px] font-medium text-ink-500">{hint}</span>}
      {aside && <div className="ml-auto">{aside}</div>}
    </div>
  )
}

// The source repository's web page, opened in a new window.
export function RepositoryLink({ href }: { href: string }) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="inline-flex shrink-0 items-center gap-1 text-[13px] font-medium text-riso-blue underline outline-none focus-visible:shadow-focus">
      <ExternalLinkIcon aria-hidden="true" className="size-3.5" />
      {text.repository}
    </a>
  )
}

export function Quiet({ children }: { children: ReactNode }) {
  return <p className="py-1.5 text-[13px] text-ink-500">{children}</p>
}
