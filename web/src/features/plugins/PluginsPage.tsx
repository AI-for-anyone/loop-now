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

// Host status strip: status, number of loaded plugins, start time, events handled today; when the host has stopped, it shows what it was
// last handling before the crash and offers a restart.
// On the right is the one-click switch that disables all plugins.
function HostStrip() {
  const host = usePluginHost()
  const settings = usePluginSettings()
  const restart = useRestartPluginHost()
  const save = useSavePluginSettings()
  const data = host.data
  const disabled = settings.data?.disabled ?? false
  const crash = data?.lastCrash
  return (
    // Redesign: placed in the page header's meta info as a 1.5px dashed box with text one size smaller; when stopped, what it was handling
    // before the crash goes on the box's second line.
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

// One installed plugin: name, id and version, source; what each side contains (from the manifest in the details); the number of projects
// that enable it and of Agents it is granted to; status and links.
function InstalledCard({ plugin, installer }: { plugin: Schemas['Plugin']; installer: ReturnType<typeof usePluginInstaller> }) {
  const detail = usePlugin(plugin.id)
  const markets = usePluginMarkets()
  const entry = marketEntryOf(markets.data?.markets, plugin)
  const manifest = detail.data?.manifest
  const agentHalf = manifest ? hasAgentHalf(manifest) : plugin.grantedAgents > 0
  return (
    // Redesign: a card with a white background, a 2px navy outline and a fluorescent-pink solid offset shadow; on the left, the initial
    // letter on a yellow block (decorative), and the name in the display font.
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

// Official plugins that ship with the platform and aren't installed yet (to be replaced by official market cards in phase three).
function BuiltinSection({ installed }: { installed: string[] }) {
  const navigate = useNavigate()
  const sources = useBuiltinPluginSources()
  const installer = usePluginInstaller((plugin) => void navigate(pluginPath(plugin.id)))
  const rest = (sources.data?.sources ?? []).filter((source) => !installed.includes(source.id))
  if (sources.isPending || (sources.isSuccess && rest.length === 0)) return null
  return (
    // Redesign: a dashed line above, the title in ZCOOL with a yellow highlighter mark; a color band with halftone dots across the top of
    // each card (cycling blue, pink, yellow).
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

// Plugin market (global; step 4 of the "printed sheet" redesign): the page header is the shared page title, next to it the host status
// (including the one-click disable) and “安装插件” (Install plugin);
// the three categories (in the URL as ?tab=) are index tabs on the sheet's top edge: Installed, Official market, Other markets. The install
// entry point additionally has three sources: official, git and local folder.
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
        {/* The scroll area is the positioning container: absolutely positioned elements inside it (such as sr-only text) scroll with the
            content instead of stretching the whole page based on their unscrolled position. */}
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
