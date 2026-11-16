import type { ReactNode } from 'react'
import { ExternalLinkIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { pluginTrust, usePluginMarkets } from '@/api/plugins'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// Small parts shared by the plugin pages: source badge, id and version, status, card frame.

const text = t.plugins

export const cardClass = 'rounded-panel border-2 border-ink bg-white'

// Plugin card (redesign): a card with a white background, a 2px navy outline and a navy solid offset shadow, with a color band with
// halftone dots across the top, cycling blue, pink, yellow (the nth card takes the nth color).
const BANDS = ['blue', 'pink', 'yellow'] as const
const bandFill: Record<(typeof BANDS)[number], string> = {
  blue: 'bg-riso-blue bg-[radial-gradient(rgb(255_255_255/.32)_1.2px,transparent_1.5px)]',
  pink: 'bg-riso-pink bg-[radial-gradient(rgb(255_255_255/.32)_1.2px,transparent_1.5px)]',
  yellow: 'bg-riso-yellow bg-[radial-gradient(rgb(24_38_94/.28)_1.2px,transparent_1.5px)]',
}
export const bandCardClass = 'mr-[5px] mb-[5px] flex min-w-0 flex-col overflow-hidden rounded-card border-2 border-ink bg-white shadow-[5px_5px_0_var(--ink)]'
export function CardBand({ index }: { index: number }) {
  const band = BANDS[index % BANDS.length]
  return <span aria-hidden="true" data-band={band} className={cn('block h-11 shrink-0 border-b-2 border-ink bg-size-[8px_8px]', bandFill[band])} />
}
// Install and update buttons on cards: pills with a white background, a navy outline and a navy offset shadow.
export const cardButtonClass = 'border-2 border-ink bg-white shadow-[3px_3px_0_var(--ink)] hover:bg-paper-raised'

export function IdVersion({ id, version }: { id: string; version: string }) {
  return <code className="rounded-[6px] bg-ink/[0.06] px-2 py-0.5 font-mono text-[12px] leading-[18px] font-bold text-ink">{text.idVersion(id, version)}</code>
}

// Official plugins (shipped with the platform, or installed from the official market) get a navy pill with white text; ones a market
// marks as verified get a green background; all other sources haven't been reviewed and get a yellow background naming the source.
// When trust is omitted, judge by the source alone (shipped with the platform means official); when it is undefined, it isn't known yet
// (the market list hasn't been read), so show nothing for now.
export function SourceBadge({ source, trust = source.kind === 'builtin' ? 'official' : null }: { source: Pick<Schemas['PluginResolvedSource'], 'kind'>; trust?: 'official' | 'verified' | null }) {
  // Redesign: “官方” (Official) is a pill with white text on navy (identity markers are always white on navy).
  if (trust === 'official')
    return <span className="rounded-full bg-ink px-2 text-[11.5px] leading-5 font-black text-white">{text.sources.builtin}</span>
  if (trust === 'verified') return <span className="rounded-[6px] bg-tint-green px-1.5 py-px text-[11.5px] leading-4 font-black text-green-text">{text.verifiedSource(text.sources[source.kind])}</span>
  return <span className="rounded-[6px] bg-tint-yellow px-1.5 py-px text-[11.5px] leading-4 font-black text-ink">{text.unverified(text.sources[source.kind])}</span>
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

export function StatusLabel({ status, className }: { status: Schemas['PluginStatus']; className?: string }) {
  const tone = statusTone[status]
  return (
    <span className={cn('inline-flex items-center gap-1.5 text-[13px] font-black whitespace-nowrap', tone.text, className)}>
      <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', tone.dot)} />
      {text.status[status]}
    </span>
  )
}

// A small heading inside a card: display font, followed by a line of gray explanation.
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

// The source repository's web page, opened in a new window.
export function RepositoryLink({ href }: { href: string }) {
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" className="inline-flex shrink-0 items-center gap-1 text-[13px] font-bold text-riso-blue underline outline-none focus-visible:shadow-focus">
      <ExternalLinkIcon aria-hidden="true" className="size-3.5" />
      {text.repository}
    </a>
  )
}

export function Quiet({ children }: { children: ReactNode }) {
  return <p className="py-1.5 text-[13px] text-ink-500">{children}</p>
}
