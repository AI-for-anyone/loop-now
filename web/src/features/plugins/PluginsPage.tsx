import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import type { Schemas } from '@/api/client'
import { marketEntryOf, marketSource, useBuiltinPluginSources, usePlugin, usePluginHost, usePluginMarkets, usePlugins, usePluginSettings, useRestartPluginHost, useSavePluginSettings } from '@/api/plugins'
import { ErrorAlert } from '@/components/ErrorAlert'
import { SectionError } from '@/components/RequestError'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { IndexTabs } from '@/components/IndexTabs'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { Sheet } from '@/components/Sheet'
import { indexTabClass } from '@/lib/index-tab'
import { cn } from '@/lib/utils'
import { InstallSourceDialog } from './InstallFlow'
import { MarketPlugins } from './Markets'
import { Chips } from './ManifestSummary'
import { componentChips, hasAgentHalf } from './manifest'
import { bandCardClass, CardBand, cardButtonClass, IdVersion, InstalledSourceBadge, Quiet, SourceBadge, StatusLabel } from './parts'
import { pluginPath } from './paths'
import { usePluginInstaller } from './usePluginInstaller'

const text = t.plugins

const hostDot: Record<Schemas['PluginHostStatus']['state'], string> = {
  running: 'bg-[var(--status-done)]',
  starting: 'bg-[var(--status-interrupt)]',
  restarting: 'bg-[var(--status-interrupt)]',
  idle: 'bg-ink-500',
  stopped: 'bg-[var(--status-fail)]',
  disabled: 'bg-ink-500',
}

// 宿主状态条：状态、已加载的插件数、启动时间、今天处理的事件；宿主停了时写出崩溃前最后在处理什么，并可重新启动。
// 右侧是一键停用全部插件。
function HostStrip() {
  const host = usePluginHost()
  const settings = usePluginSettings()
  const restart = useRestartPluginHost()
  const save = useSavePluginSettings()
  const data = host.data
  const disabled = settings.data?.disabled ?? false
  const crash = data?.lastCrash
  return (
    // 改版：放在页头的元信息里，是一个 1.5px 虚线框，字小一号；停了时崩溃前在处理什么写在框里第二行。
    <section aria-label={text.host.label} className="flex min-w-0 flex-col gap-1.5 rounded-[4px] border-[1.5px] border-dashed border-ink/45 px-2.5 py-1">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-[12.5px]">
        {data ? (
          <>
            <span role="status" className="inline-flex items-center gap-1.5 font-black text-ink">
              <span aria-hidden="true" className={cn('size-[7px] rounded-full', hostDot[data.state])} />
              {data.configured ? text.host.state[data.state] : text.host.notConfigured}
            </span>
            {data.configured && data.state !== 'disabled' && (
              <span className="text-ink-700">
                {[text.host.loaded(data.loaded.length), data.startedAt && text.host.startedAt(t.chat.time(data.startedAt)), data.today && text.host.today(data.today.delivered, data.today.failed)].filter(Boolean).join(' · ')}
              </span>
            )}
            {data.state === 'stopped' && (
              <Button size="xs" variant="ink" disabled={restart.isPending || disabled} onClick={() => restart.mutate()}>
                {restart.isPending ? text.host.restarting : text.host.restart}
              </Button>
            )}
          </>
        ) : (
          <span className="text-ink-500">{host.isError ? null : t.common.loading}</span>
        )}
        <label className="inline-flex items-center gap-2 font-black text-ink" title={text.host.killSwitchHint}>
          <Switch checked={disabled} disabled={!settings.data || save.isPending} onCheckedChange={(next) => settings.data && save.mutate({ ...settings.data, disabled: next })} />
          {text.host.killSwitch}
        </label>
      </div>
      {crash && data?.state === 'stopped' && (
        <p className="text-[12.5px] leading-snug text-red-text [overflow-wrap:anywhere]">
          {[text.host.crashed(t.chat.time(crash.at)), crash.error, crash.last?.plugin && text.host.lastRequest(crash.last.plugin, crash.last.eventType ?? crash.last.type)].filter(Boolean).join(' · ')}
        </p>
      )}
      <ErrorAlert error={host.error ?? settings.error ?? restart.error ?? save.error} />
    </section>
  )
}

// 已安装的一个插件：名称、id 与版本、来源；两侧各有什么（来自详情里的清单）；启用的项目数与授权的 Agent 数；状态与入口。
function InstalledCard({ plugin, installer }: { plugin: Schemas['Plugin']; installer: ReturnType<typeof usePluginInstaller> }) {
  const detail = usePlugin(plugin.id)
  const markets = usePluginMarkets()
  const entry = marketEntryOf(markets.data?.markets, plugin)
  const manifest = detail.data?.manifest
  const agentHalf = manifest ? hasAgentHalf(manifest) : plugin.grantedAgents > 0
  return (
    // 改版：白底、2px 藏青描边、荧光粉实色投影的卡片；左边一块黄底的首字母（装饰），名称是展示字。
    <li className="mr-1 mb-1 grid min-w-0 gap-x-5 gap-y-3 rounded-card border-2 border-ink bg-white px-[18px] py-3.5 shadow-print-pink md:grid-cols-[auto_minmax(0,1fr)_200px_auto] md:items-center">
      <span aria-hidden="true" className="hidden size-12 items-center justify-center rounded-[10px] border-2 border-ink bg-riso-yellow font-display-latin text-[20px] font-extrabold md:flex">
        {localizedText(plugin.displayName).slice(0, 1).toUpperCase()}
      </span>
      <div className="grid min-w-0 gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h3 className="font-display-cn text-[26px] leading-tight font-normal [overflow-wrap:anywhere]">
            <Link to={pluginPath(plugin.id)} className="outline-none hover:underline focus-visible:shadow-focus">
              {localizedText(plugin.displayName)}
            </Link>
          </h3>
          <IdVersion id={plugin.id} version={plugin.version} />
          <InstalledSourceBadge plugin={plugin} />
        </div>
        {manifest && <Chips items={componentChips(manifest)} />}
        {plugin.status === 'error' && plugin.lastError && <p className="text-[12.5px] leading-snug font-bold text-red-text [overflow-wrap:anywhere]">{plugin.lastError}</p>}
      </div>
      <p className="grid text-[13px] text-ink-700">
        <span>{text.enabledProjects(plugin.enabledProjects)}</span>
        <span>{agentHalf ? text.grantedAgents(plugin.grantedAgents) : text.noAgentHalf}</span>
      </p>
      <div className="flex items-center gap-3 md:flex-col md:items-end">
        <StatusLabel status={plugin.status} />
        {entry?.updateAvailable && plugin.source.market && (
          <Button size="xs" disabled={installer.pending !== null} onClick={() => void installer.begin(plugin.id, { source: marketSource(plugin.source.market!, entry) })}>
            {installer.pending === plugin.id ? text.installEntry.reading : text.markets.updateTo(entry.version)}
          </Button>
        )}
        <Button asChild size="xs" variant="secondary" className={cn('ml-auto md:ml-0', cardButtonClass)}>
          <Link to={plugin.status === 'error' ? pluginPath(plugin.id, 'logs') : pluginPath(plugin.id)}>{plugin.status === 'error' ? text.viewLogs : text.details}</Link>
        </Button>
      </div>
    </li>
  )
}

// 随平台发布、还没安装的官方插件（第三阶段换成官方市场的卡片）。
function BuiltinSection({ installed }: { installed: string[] }) {
  const navigate = useNavigate()
  const sources = useBuiltinPluginSources()
  const installer = usePluginInstaller((plugin) => void navigate(pluginPath(plugin.id)))
  const rest = (sources.data?.sources ?? []).filter((source) => !installed.includes(source.id))
  if (sources.isPending || (sources.isSuccess && rest.length === 0)) return null
  return (
    // 改版：上方一道虚线，标题是 ZCOOL 加黄色记号笔；卡片顶上一条带网点的色带（蓝、粉、黄轮换）。
    <section aria-labelledby="builtin-title" className="flex flex-col gap-[18px] border-t-2 border-dashed border-ink/[0.22] pt-6">
      <h2 id="builtin-title" className="riso-marker self-start px-1.5 font-display-cn text-[30px] leading-[1.25] font-normal">
        {text.builtinTitle}
      </h2>
      <ErrorAlert error={sources.error} />
      <ul className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {rest.map((source, index) => (
          <li key={source.id} className={bandCardClass}>
            <CardBand index={index} />
            <div className="flex flex-1 flex-col gap-2 px-[18px] pt-3.5 pb-4">
              <div className="flex flex-wrap items-center gap-2">
                <h3 className="font-display-cn text-[24px] leading-tight font-normal">{localizedText(source.displayName)}</h3>
                <SourceBadge source={{ kind: 'builtin' }} />
              </div>
              <p className="text-[13px] leading-[1.7] text-ink-700">{localizedText(source.description)}</p>
              <div className="mt-auto flex items-center justify-between gap-3 pt-1">
                <span className="font-mono text-[12px] text-ink-700">v{source.version}</span>
                <Button size="xs" variant="secondary" className={cardButtonClass} disabled={installer.pending !== null} onClick={() => void installer.begin(source.id, { source: { kind: 'builtin', location: source.id } })}>
                  {installer.pending === source.id ? text.installEntry.reading : text.install}
                </Button>
              </div>
              <ErrorAlert error={installer.errorFor(source.id)} />
            </div>
          </li>
        ))}
      </ul>
      {installer.dialog}
    </section>
  )
}

type MarketTab = 'installed' | 'official' | 'other'
const MARKET_TABS: MarketTab[] = ['installed', 'official', 'other']

// 插件市场（全局，改版“一张印出来的纸”第 4 步）：页头是共用的页面标题，旁边是宿主状态（含一键停用）与“安装插件”；
// 三个分类（写在地址里的 ?tab=）是纸张上沿的索引标签：已安装、官方市场、其他市场。安装入口另有官方、git、本地目录三种来源。
export function PluginsPage() {
  const navigate = useNavigate()
  const [search] = useSearchParams()
  const plugins = usePlugins()
  const [installing, setInstalling] = useState(false)
  const onInstalled = (plugin: Schemas['Plugin']) => void navigate(pluginPath(plugin.id))
  const installer = usePluginInstaller(onInstalled)
  const list = plugins.data?.plugins ?? []
  const installed = list.map((plugin) => plugin.id)
  const tab: MarketTab = MARKET_TABS.includes(search.get('tab') as MarketTab) ? (search.get('tab') as MarketTab) : 'installed'
  return (
    <PageFrame>
      <PageHeader crumbs={[{ label: t.shell.global }]} title={text.title}>
        <HostStrip />
        <Button variant="interrupt" size="xs" className="border-2 border-ink" onClick={() => setInstalling(true)}>
          {text.installEntry.open}
        </Button>
      </PageHeader>
      <IndexTabs aria-label={text.title}>
        {MARKET_TABS.map((value) => (
          <Link key={value} to={value === 'installed' ? '/plugins' : `/plugins?tab=${value}`} aria-current={value === tab ? 'page' : undefined} className={indexTabClass(value === tab)}>
            {value === 'installed' ? text.tabs.installed(plugins.data ? list.length : null) : text.tabs[value]}
          </Link>
        ))}
      </IndexTabs>
      <Sheet className="flex-1">
        {/* 滚动区是定位容器：里面绝对定位的元素（如 sr-only 说明）随内容滚动，不会按未滚动时的位置把整页撑高。 */}
        <div className="relative min-h-0 flex-1 overflow-y-auto">
          <div className="flex max-w-[1180px] flex-col gap-6 pt-6 pr-[clamp(14px,3vw,40px)] pb-16 pl-[clamp(12px,2vw,30px)]">
            {tab === 'installed' && (
              <section aria-label={text.installedBadge} className="flex flex-col gap-3">
                {plugins.isError ? (
                  <SectionError title={text.title} error={plugins.error} onRetry={() => void plugins.refetch()} />
                ) : plugins.isPending ? (
                  <Quiet>{text.loading}</Quiet>
                ) : list.length === 0 ? (
                  <Quiet>{text.empty}</Quiet>
                ) : (
                  <ul className="flex flex-col gap-3.5">
                    {list.map((plugin) => (
                      <InstalledCard key={plugin.id} plugin={plugin} installer={installer} />
                    ))}
                  </ul>
                )}
              </section>
            )}
            {tab === 'installed' && plugins.isSuccess && <BuiltinSection installed={installed} />}
            {tab === 'official' && <MarketPlugins official />}
            {tab === 'other' && <MarketPlugins official={false} />}
          </div>
        </div>
      </Sheet>
      {installer.dialog}
      <InstallSourceDialog open={installing} onOpenChange={setInstalling} installed={installed} onInstalled={onInstalled} />
    </PageFrame>
  )
}
