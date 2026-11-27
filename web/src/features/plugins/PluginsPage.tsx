import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { PlusIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { marketEntryOf, marketSource, usePluginHost, usePluginMarkets, usePlugins, usePluginSettings, useRestartPluginHost, useSavePluginSettings } from '@/api/plugins'
import { ErrorAlert } from '@/components/ErrorAlert'
import { IndexTabs } from '@/components/IndexTabs'
import { PageFrame, PageHeader } from '@/components/PageHeader'
import { SectionError } from '@/components/RequestError'
import { PageBody, Sheet } from '@/components/Sheet'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { t } from '@/i18n'
import { indexTabClass } from '@/lib/index-tab'
import { localizedText } from '@/lib/localized-text'
import { cn } from '@/lib/utils'
import { InstallSourceDialog } from './InstallFlow'
import { DiscoverCatalog, type DiscoverFilter } from './Markets'
import { cardButtonClass, IdVersion, InstalledSourceBadge, Quiet, StatusLabel, surfaceListClass } from './parts'
import { pluginActivityPath, pluginPath } from './paths'
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

// Compact single-line host strip for the right-aligned header actions slot: status dot, loaded count, and "Disable all" switch.
function HostStrip({
  host,
  settings,
  save,
}: {
  host: ReturnType<typeof usePluginHost>
  settings: ReturnType<typeof usePluginSettings>
  save: ReturnType<typeof useSavePluginSettings>
}) {
  const data = host.data
  const disabled = settings.data?.disabled ?? false
  const hoverDetails = data?.configured && data.state !== 'disabled'
    ? [
        data.startedAt && text.host.startedAt(t.chat.time(data.startedAt)),
        data.today && text.host.today(data.today.delivered, data.today.failed),
      ]
        .filter(Boolean)
        .join(' · ')
    : undefined

  return (
    <section
      aria-label={text.host.label}
      className="flex min-w-0 items-center gap-2.5 rounded-ticket border border-hairline-strong bg-surface-subtle px-2.5 py-1 text-[12px]"
    >
      {data ? (
        <span
          role="status"
          title={hoverDetails || undefined}
          className="inline-flex items-center gap-1.5 font-semibold text-ink"
        >
          <span aria-hidden="true" className={cn('size-[7px] shrink-0 rounded-full', hostDot[data.state])} />
          <span>{data.configured ? text.host.state[data.state] : text.host.notConfigured}</span>
          {data.configured && data.state !== 'disabled' && (
            <span className="font-normal text-ink-500">· {text.host.loaded(data.loaded.length)}</span>
          )}
        </span>
      ) : (
        <span className="text-ink-500">{host.isError ? null : t.common.loading}</span>
      )}
      <span aria-hidden="true" className="h-3.5 w-px bg-hairline-strong" />
      <label
        className="inline-flex cursor-pointer items-center gap-1.5 font-medium text-ink"
        title={text.host.killSwitchHint}
      >
        <Switch
          checked={disabled}
          disabled={!settings.data || save.isPending}
          onCheckedChange={(next) => settings.data && save.mutate({ ...settings.data, disabled: next })}
        />
        <span>{text.host.killSwitch}</span>
      </label>
    </section>
  )
}

// Crash / error banner rendered in PageHeader's note band only when the host has stopped or a host/settings request fails.
function HostCrashBanner({
  host,
  settings,
  restart,
  save,
}: {
  host: ReturnType<typeof usePluginHost>
  settings: ReturnType<typeof usePluginSettings>
  restart: ReturnType<typeof useRestartPluginHost>
  save: ReturnType<typeof useSavePluginSettings>
}) {
  const data = host.data
  const disabled = settings.data?.disabled ?? false
  const crash = data?.lastCrash
  const error = host.error ?? settings.error ?? restart.error ?? save.error
  if (data?.state !== 'stopped' && !error) return null
  return (
    <div className="flex flex-col gap-1.5">
      {data?.state === 'stopped' && (
        <div role="alert" className="flex flex-wrap items-center justify-between gap-2.5 text-[12.5px] text-red-text">
          <span className="min-w-0 [overflow-wrap:anywhere]">
            {crash
              ? [
                  text.host.crashed(t.chat.time(crash.at)),
                  crash.error,
                  crash.last?.plugin && text.host.lastRequest(crash.last.plugin, crash.last.eventType ?? crash.last.type),
                ]
                  .filter(Boolean)
                  .join(' · ')
              : text.host.state.stopped}
          </span>
          <Button size="xs" variant="ink" disabled={restart.isPending || disabled} onClick={() => restart.mutate()}>
            {restart.isPending ? text.host.restarting : text.host.restart}
          </Button>
        </div>
      )}
      <ErrorAlert error={error} />
    </div>
  )
}

const summaryChipClass =
  'rounded-stamp border border-hairline bg-surface-subtle px-2 py-0.5 text-[11.5px] leading-4 font-medium text-ink-700'

// Compact installed plugin row inside the single-surface divide-y list: renders purely from the list payload (zero N+1 detail fetches).
function InstalledRow({
  plugin,
  markets,
  installer,
}: {
  plugin: Schemas['Plugin']
  markets: Schemas['PluginMarket'][] | undefined
  installer: ReturnType<typeof usePluginInstaller>
}) {
  const entry = marketEntryOf(markets, plugin)
  const showAgentCount = !plugin.hasPlatform || plugin.grantedAgents > 0
  return (
    <li className="flex min-w-0 flex-wrap items-center justify-between gap-x-4 gap-y-2.5 px-4 py-3">
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h3 className="text-[14.5px] leading-snug font-semibold tracking-tight [overflow-wrap:anywhere]">
            <Link to={pluginPath(plugin.id)} className="outline-none hover:underline focus-visible:shadow-focus">
              {localizedText(plugin.displayName)}
            </Link>
          </h3>
          <IdVersion id={plugin.id} version={plugin.version} />
          <InstalledSourceBadge plugin={plugin} />
        </div>
        <div className="flex flex-wrap items-center gap-1.5">
          <span className={summaryChipClass}>{plugin.hasPlatform ? text.chips.platform : text.chips.agent}</span>
          <span className={summaryChipClass}>{text.enabledProjects(plugin.enabledProjects)}</span>
          <span className={summaryChipClass}>
            {showAgentCount ? text.grantedAgents(plugin.grantedAgents) : text.noAgentHalf}
          </span>
        </div>
        {plugin.status === 'error' && plugin.lastError && (
          <p className="text-[12.5px] leading-snug font-medium text-red-text [overflow-wrap:anywhere]">
            {plugin.lastError}
          </p>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-2.5">
        <StatusLabel status={plugin.status} />
        {entry?.updateAvailable && plugin.source.market && (
          <Button
            size="xs"
            disabled={installer.pending !== null}
            onClick={() => void installer.begin(plugin.id, { source: marketSource(plugin.source.market!, entry) })}
          >
            {installer.pending === plugin.id ? text.installEntry.reading : text.markets.updateTo(entry.version)}
          </Button>
        )}
        <Button asChild size="xs" variant="secondary" className={cardButtonClass}>
          <Link to={plugin.status === 'error' ? pluginActivityPath(plugin.id, 'logs') : pluginPath(plugin.id)}>
            {plugin.status === 'error' ? text.viewLogs : text.details}
          </Link>
        </Button>
      </div>
    </li>
  )
}

type MarketTab = 'installed' | 'discover'

function resolveTab(rawTab: string | null): { tab: MarketTab; discoverFilter: DiscoverFilter } {
  if (rawTab === 'official') return { tab: 'discover', discoverFilter: 'official' }
  if (rawTab === 'other') return { tab: 'discover', discoverFilter: 'community' }
  if (rawTab === 'discover') return { tab: 'discover', discoverFilter: 'all' }
  return { tab: 'installed', discoverFilter: 'all' }
}

// Plugin market (global): slim header with host strip and "+ Install plugin" action, two tabs ("Installed" | "Discover"), and
// scrollable PageBody with automatic floating ScrollRuler.
export function PluginsPage() {
  const navigate = useNavigate()
  const [search] = useSearchParams()
  const plugins = usePlugins()
  const markets = usePluginMarkets()
  const host = usePluginHost()
  const settings = usePluginSettings()
  const restart = useRestartPluginHost()
  const save = useSavePluginSettings()
  const [installing, setInstalling] = useState(false)
  const onInstalled = (plugin: Schemas['Plugin']) => void navigate(pluginPath(plugin.id))
  const installer = usePluginInstaller(onInstalled)

  const list = plugins.data?.plugins ?? []
  const installed = list.map((plugin) => plugin.id)
  const { tab, discoverFilter } = resolveTab(search.get('tab'))
  const hasHostBanner = host.data?.state === 'stopped' || Boolean(host.error ?? settings.error ?? restart.error ?? save.error)

  return (
    <PageFrame>
      <PageHeader
        crumbs={[{ label: t.shell.global }]}
        title={text.title}
        actions={
          <>
            <HostStrip host={host} settings={settings} save={save} />
            <Button
              variant="default"
              size="xs"
              onClick={() => setInstalling(true)}
            >
              <PlusIcon aria-hidden="true" className="size-3.5" />
              {text.installEntry.open}
            </Button>
          </>
        }
        note={hasHostBanner ? <HostCrashBanner host={host} settings={settings} restart={restart} save={save} /> : undefined}
      />
      <IndexTabs aria-label={text.title}>
        <Link
          to="/plugins"
          aria-current={tab === 'installed' ? 'page' : undefined}
          className={indexTabClass(tab === 'installed')}
        >
          <span>{text.tabs.installedLabel}</span>
          {plugins.data && (
            <span className="font-mono text-[11px] font-medium text-ink-500 tabular-nums">
              {list.length}
            </span>
          )}
        </Link>
        <Link
          to="/plugins?tab=discover"
          aria-current={tab === 'discover' ? 'page' : undefined}
          className={indexTabClass(tab === 'discover')}
        >
          <span>{text.tabs.discover}</span>
        </Link>
      </IndexTabs>
      <Sheet className="flex-1">
        <PageBody width="default" innerClassName="pb-12">
          {tab === 'installed' ? (
            <section aria-label={text.installedBadge} className="flex flex-col gap-3">
              {plugins.isError ? (
                <SectionError title={text.title} error={plugins.error} onRetry={() => void plugins.refetch()} />
              ) : plugins.isPending ? (
                <Quiet>{text.loading}</Quiet>
              ) : list.length === 0 ? (
                <div className="flex flex-col items-start gap-3 rounded-card border border-hairline bg-surface-subtle/50 px-5 py-6">
                  <p className="text-[13.5px] text-ink-700">{text.empty}</p>
                  <Button asChild size="xs" variant="secondary">
                    <Link to="/plugins?tab=discover">{text.markets.browseDiscover}</Link>
                  </Button>
                </div>
              ) : (
                <ul className={surfaceListClass}>
                  {list.map((plugin) => (
                    <InstalledRow key={plugin.id} plugin={plugin} markets={markets.data?.markets} installer={installer} />
                  ))}
                </ul>
              )}
            </section>
          ) : (
            <DiscoverCatalog installed={list} initialFilter={discoverFilter} />
          )}
        </PageBody>
      </Sheet>
      {installer.dialog}
      <InstallSourceDialog open={installing} onOpenChange={setInstalling} installed={installed} onInstalled={onInstalled} />
    </PageFrame>
  )
}
