import type { ReactNode } from 'react'
import { ExternalLinkIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { pluginTrust, usePluginMarkets } from '@/api/plugins'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

// 插件页面共用的小部件：来源标签、id 与版本、状态、卡片外框。

const text = t.plugins

export const cardClass = 'rounded-panel border-2 border-ink bg-white'

// 插件卡（改版）：白底、2px 藏青描边、藏青实色投影的卡片，顶上一条带网点的色带，按蓝、粉、黄轮换（第几张卡取第几种）。
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
// 卡片上的安装、更新按钮：白底、藏青描边加藏青投影的胶囊。
export const cardButtonClass = 'border-2 border-ink bg-white shadow-[3px_3px_0_var(--ink)] hover:bg-paper-raised'

export function IdVersion({ id, version }: { id: string; version: string }) {
  return <code className="rounded-[6px] bg-ink/[0.06] px-2 py-0.5 font-mono text-[12px] leading-[18px] font-bold text-ink">{text.idVersion(id, version)}</code>
}

// 官方（随平台发布，或从官方市场装的）用粉色贴纸；市场标为已验证的用绿底；其余来源没有经过审核，用黄底写明来源。
// trust 缺省时只按来源判断（随平台发布的为官方）；为 undefined 时还不知道（市场列表没读到），先不显示。
export function SourceBadge({ source, trust = source.kind === 'builtin' ? 'official' : null }: { source: Pick<Schemas['PluginResolvedSource'], 'kind'>; trust?: 'official' | 'verified' | null }) {
  // 改版：“官方”是藏青底白字的胶囊（身份标记一律藏青底白字）。
  if (trust === 'official')
    return <span className="rounded-full bg-ink px-2 text-[11.5px] leading-5 font-black text-white">{text.sources.builtin}</span>
  if (trust === 'verified') return <span className="rounded-[6px] bg-tint-green px-1.5 py-px text-[11.5px] leading-4 font-black text-green-text">{text.verifiedSource(text.sources[source.kind])}</span>
  return <span className="rounded-[6px] bg-tint-yellow px-1.5 py-px text-[11.5px] leading-4 font-black text-ink">{text.unverified(text.sources[source.kind])}</span>
}

// 已安装插件的来源标签：从市场装的按市场判断（官方市场、已验证的条目算审阅过），市场列表读到前不显示；读取失败时按未审阅。
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

// 来源仓库的网页，新窗口打开。
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
