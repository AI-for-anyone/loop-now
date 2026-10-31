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
import { pageTitleClass } from '@/lib/page-title'
import { cn } from '@/lib/utils'
import { InstallSourceDialog } from './InstallFlow'
import { MarketPlugins } from './Markets'
import { Chips } from './ManifestSummary'
import { componentChips, hasAgentHalf } from './manifest'
import { cardClass, IdVersion, Quiet, SourceBadge, StatusLabel } from './parts'
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
    <section aria-label={text.host.label} className="flex flex-col gap-2 rounded-panel border-2 border-dashed border-ink bg-paper-raised px-4 py-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-[13px]">
        {data ? (
          <>
            <span role="status" className="inline-flex items-center gap-1.5 font-black">
              <span aria-hidden="true" className={cn('size-2 rounded-full', hostDot[data.state])} />
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
        <label className="ml-auto inline-flex items-center gap-2 font-black" title={text.host.killSwitchHint}>
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

// 已安装的一个插件：名称、id 与版本、来源；两半各有什么（来自详情里的清单）；启用的项目数与授权的 Agent 数；状态与入口。
function InstalledCard({ plugin, installer }: { plugin: Schemas['Plugin']; installer: ReturnType<typeof usePluginInstaller> }) {
  const detail = usePlugin(plugin.id)
  const markets = usePluginMarkets()
  const entry = marketEntryOf(markets.data?.markets, plugin)
  const manifest = detail.data?.manifest
  const agentHalf = manifest ? hasAgentHalf(manifest) : plugin.grantedAgents > 0
  return (
    <li className={cn(cardClass, 'grid min-w-0 gap-x-6 gap-y-3 px-4 py-3.5 md:grid-cols-[minmax(0,1fr)_200px_auto] md:items-center')}>
      <div className="grid min-w-0 gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h3 className="font-display-cn text-[21px] leading-tight font-normal [overflow-wrap:anywhere]">
            <Link to={pluginPath(plugin.id)} className="outline-none hover:underline focus-visible:shadow-focus">
              {localizedText(plugin.displayName)}
            </Link>
          </h3>
          <IdVersion id={plugin.id} version={plugin.version} />
          <SourceBadge source={plugin.source} />
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
        <Button asChild size="xs" variant="secondary" className="ml-auto md:ml-0">
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
    <section aria-labelledby="builtin-title" className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <h2 id="builtin-title" className="font-display-cn text-[18px] whitespace-nowrap text-ink-500">
          {text.builtinTitle}
        </h2>
        <span aria-hidden="true" className="flex-1 border-t-2 border-dashed border-ink/25" />
      </div>
      <ErrorAlert error={sources.error} />
      <ul className="grid gap-3.5 md:grid-cols-2 xl:grid-cols-3">
        {rest.map((source) => (
          <li key={source.id} className={cn(cardClass, 'flex min-w-0 flex-col gap-2 px-4 py-3.5')}>
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="font-display-cn text-[19px] leading-tight font-normal">{localizedText(source.displayName)}</h3>
              <SourceBadge source={{ kind: 'builtin' }} />
            </div>
            <p className="text-[13px] leading-snug text-ink-700">{localizedText(source.description)}</p>
            <div className="mt-auto flex items-center justify-between gap-3 pt-1">
              <span className="font-mono text-[12px] text-ink-500">v{source.version}</span>
              <Button size="xs" variant="secondary" disabled={installer.pending !== null} onClick={() => void installer.begin(source.id, { source: { kind: 'builtin', location: source.id } })}>
                {installer.pending === source.id ? text.installEntry.reading : text.install}
              </Button>
            </div>
            <ErrorAlert error={installer.errorFor(source.id)} />
          </li>
        ))}
      </ul>
      {installer.dialog}
    </section>
  )
}

type MarketTab = 'installed' | 'official' | 'other'
const MARKET_TABS: MarketTab[] = ['installed', 'official', 'other']

const tabClass = (active: boolean) =>
  cn(
    'inline-flex h-9 items-center rounded-full border-2 px-4 text-[13.5px] font-black whitespace-nowrap outline-none focus-visible:shadow-focus',
    active ? 'border-ink bg-riso-yellow text-ink shadow-print-ink' : 'border-transparent text-ink-700 hover:bg-tint-blue hover:text-ink',
  )

// 插件市场（全局）：宿主状态与一键停用；三个页签（写在地址里的 ?tab=）：已安装、官方市场、其他市场。安装入口另有官方、git、本地目录三种来源。
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
  // 滚动区是定位容器：里面绝对定位的元素（如 sr-only 说明）随内容滚动，不会按未滚动时的位置把整页撑高。
  return (
    <div className="relative h-full overflow-y-auto">
      <div className="mx-auto flex max-w-[1180px] flex-col gap-4 px-4 pt-[22px] pb-16 sm:px-8">
        <header className="flex flex-wrap items-end gap-x-3.5 gap-y-3">
          <div className="flex flex-col gap-1.5">
            <span className="text-[12px] font-bold text-ink-500">{`${text.eyebrow} · ${text.title}`}</span>
            <h1 className={pageTitleClass}>{text.title}</h1>
          </div>
          <Button className="sm:ml-auto" onClick={() => setInstalling(true)}>
            {text.installEntry.open}
          </Button>
        </header>
        <HostStrip />
        <nav aria-label={text.title} className="-mx-1 flex gap-1.5 overflow-x-auto px-1 pb-1">
          {MARKET_TABS.map((value) => (
            <Link key={value} to={value === 'installed' ? '/plugins' : `/plugins?tab=${value}`} aria-current={value === tab ? 'page' : undefined} className={tabClass(value === tab)}>
              {value === 'installed' ? text.tabs.installed(plugins.data ? list.length : null) : text.tabs[value]}
            </Link>
          ))}
        </nav>
        {tab === 'installed' && (
          <section aria-label={text.installedBadge} className="flex flex-col gap-3">
            {plugins.isError ? (
              <SectionError title={text.title} error={plugins.error} onRetry={() => void plugins.refetch()} />
            ) : plugins.isPending ? (
              <Quiet>{text.loading}</Quiet>
            ) : list.length === 0 ? (
              <Quiet>{text.empty}</Quiet>
            ) : (
              <ul className="flex flex-col gap-3">
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
      {installer.dialog}
      <InstallSourceDialog open={installing} onOpenChange={setInstalling} installed={installed} onInstalled={onInstalled} />
    </div>
  )
}
