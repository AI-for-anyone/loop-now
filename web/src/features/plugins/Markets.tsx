import { useId, useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import { SlidersHorizontalIcon } from 'lucide-react'
import type { Schemas } from '@/api/client'
import { marketSource, useBuiltinPluginSources, usePluginMarkets, usePluginSettings, useSavePluginSettings } from '@/api/plugins'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { segmentedClass, segmentedItemClass } from '@/lib/segmented'
import { cn } from '@/lib/utils'
import { bandCardClass, CardBand, cardButtonClass, Quiet, RepositoryLink, SourceBadge } from './parts'
import { pluginPath } from './paths'
import { marketGitSource, repositoryUrl } from './repository'
import { usePluginInstaller } from './usePluginInstaller'

type Market = Schemas['PluginMarket']
type Entry = Schemas['PluginMarketEntry']
type BuiltinSource = Schemas['BuiltinPluginSource']
export type DiscoverFilter = 'all' | 'official' | 'community'

const FILTERS: readonly DiscoverFilter[] = ['all', 'official', 'community']
const text = t.plugins.markets

// A built-in official plugin card inside the Discover catalog.
function BuiltinCard({
  source,
  installedPlugin,
  installer,
  index,
}: {
  source: BuiltinSource
  installedPlugin?: Schemas['Plugin']
  installer: ReturnType<typeof usePluginInstaller>
  index: number
}) {
  const key = `builtin#${source.id}`
  const busy = installer.pending !== null
  return (
    <li className={bandCardClass}>
      <CardBand index={index} />
      <div className="flex min-w-0 flex-1 flex-col gap-2 px-4 pt-3.5 pb-4">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h3 className="font-display-cn text-[16px] leading-snug font-semibold tracking-tight [overflow-wrap:anywhere] [[data-skin=plate]_&]:text-[24px] [[data-skin=plate]_&]:leading-tight [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal">
            {installedPlugin ? (
              <Link to={pluginPath(source.id)} className="outline-none hover:underline focus-visible:shadow-focus">
                {localizedText(source.displayName)}
              </Link>
            ) : (
              localizedText(source.displayName)
            )}
          </h3>
          <SourceBadge source={{ kind: 'builtin' }} />
        </div>
        <p className="text-[13px] leading-[1.65] text-ink-700 [overflow-wrap:anywhere]">{localizedText(source.description)}</p>
        <div className="mt-auto flex flex-wrap items-center justify-between gap-2 pt-1">
          <span className="font-mono text-[12px] text-ink-700">
            {installedPlugin ? text.installed(installedPlugin.version) : `v${source.version}`}
          </span>
          {installedPlugin ? (
            <span className="text-[12.5px] font-semibold text-green-text [[data-skin=plate]_&]:font-bold">{text.upToDate}</span>
          ) : (
            <Button
              size="xs"
              variant="secondary"
              className={cardButtonClass}
              disabled={busy}
              onClick={() => void installer.begin(key, { source: { kind: 'builtin', location: source.id } })}
            >
              {installer.pending === key ? t.plugins.installEntry.reading : t.plugins.install}
            </Button>
          )}
        </div>
        <ErrorAlert error={installer.errorFor(key)} />
      </div>
    </li>
  )
}

// A plugin in a market: name, official or verified badge, description, source (linked when the git repository has a web page), version;
// one not installed can be installed, an installed one shows its installed version and can be updated when there is a new version.
function EntryCard({ market, entry, installer, index }: { market: Market; entry: Entry; installer: ReturnType<typeof usePluginInstaller>; index: number }) {
  const key = `${market.url}#${entry.id}`
  const busy = installer.pending !== null
  const git = marketGitSource(entry.source)
  const repository = git && repositoryUrl(git.location, git.ref)
  return (
    <li className={bandCardClass}>
      <CardBand index={index} />
      <div className="flex min-w-0 flex-1 flex-col gap-2 px-4 pt-3.5 pb-4">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h3 className="font-display-cn text-[16px] leading-snug font-semibold tracking-tight [overflow-wrap:anywhere] [[data-skin=plate]_&]:text-[24px] [[data-skin=plate]_&]:leading-tight [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal">
            {entry.installedVersion ? (
              <Link to={pluginPath(entry.id)} className="outline-none hover:underline focus-visible:shadow-focus">
                {localizedText(entry.displayName)}
              </Link>
            ) : (
              localizedText(entry.displayName)
            )}
          </h3>
          {market.official ? (
            <SourceBadge source={{ kind: 'builtin' }} />
          ) : (
            <span className={cn('rounded-stamp px-1.5 py-px text-[11.5px] leading-4 font-semibold [[data-skin=plate]_&]:rounded-[6px] [[data-skin=plate]_&]:font-black', entry.verified ? 'bg-tint-green text-green-text' : 'bg-tint-yellow text-ink')}>
              {entry.verified ? text.verified : text.unverified}
            </span>
          )}
        </div>
        <p className="text-[13px] leading-[1.65] text-ink-700 [overflow-wrap:anywhere]">{localizedText(entry.description)}</p>
        <div className="flex min-w-0 items-center gap-3">
          <p className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-ink-500" title={entry.source}>
            {entry.source}
          </p>
          {repository && <RepositoryLink href={repository} />}
        </div>
        <div className="mt-auto flex flex-wrap items-center justify-between gap-2 pt-1">
          <span className="font-mono text-[12px] text-ink-700">{entry.installedVersion ? text.installed(entry.installedVersion) : entry.version ? `v${entry.version}` : null}</span>
          {!entry.installedVersion ? (
            <Button size="xs" variant="secondary" className={cardButtonClass} disabled={busy} onClick={() => void installer.begin(key, { source: marketSource(market.url, entry) })}>
              {installer.pending === key ? t.plugins.installEntry.reading : t.plugins.install}
            </Button>
          ) : entry.updateAvailable ? (
            <Button size="xs" disabled={busy} onClick={() => void installer.begin(key, { source: marketSource(market.url, entry) })}>
              {installer.pending === key ? t.plugins.installEntry.reading : text.updateTo(entry.version)}
            </Button>
          ) : (
            <span className="text-[12.5px] font-semibold text-green-text [[data-skin=plate]_&]:font-bold">{text.upToDate}</span>
          )}
        </div>
        <ErrorAlert error={installer.errorFor(key)} />
      </div>
    </li>
  )
}

function MarketSection({ market, installer, titled }: { market: Market; installer: ReturnType<typeof usePluginInstaller>; titled: boolean }) {
  const name = market.name ?? text.unnamed
  return (
    <section aria-label={name} className="flex min-w-0 flex-col gap-3">
      {titled && (
        <h2 className="flex min-w-0 flex-wrap items-baseline gap-x-2.5 text-[14px] font-semibold tracking-tight text-ink [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[24px] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal">
          <span className="riso-marker [[data-skin=plate]_&]:px-1.5">{name}</span>
          <span className="min-w-0 truncate font-mono text-[11.5px] font-normal text-ink-500">{market.url}</span>
        </h2>
      )}
      {market.error ? (
        <p role="alert" className="rounded-input border border-red/20 bg-tint-red px-3.5 py-2.5 text-[13px] font-semibold text-red-text [overflow-wrap:anywhere] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:font-bold">
          {text.failed(name, market.error)}
        </p>
      ) : market.plugins.length === 0 ? (
        <Quiet>{text.empty}</Quiet>
      ) : (
        <ul className="grid gap-3.5 md:grid-cols-2 xl:grid-cols-3">
          {market.plugins.map((entry, index) => (
            <EntryCard key={entry.id} market={market} entry={entry} installer={installer} index={index} />
          ))}
        </ul>
      )}
    </section>
  )
}

const isHttps = (value: string) => {
  try {
    return new URL(value).protocol === 'https:'
  } catch {
    return false
  }
}

// URLs of other markets: saved in markets of the global plugin settings (saved together with the "Disable all" kill switch).
function MarketSettings() {
  const id = useId()
  const settings = usePluginSettings()
  const save = useSavePluginSettings()
  const [url, setUrl] = useState('')
  const [invalid, setInvalid] = useState<string | undefined>()
  const markets = settings.data?.markets ?? []
  const submit = (event: FormEvent) => {
    event.preventDefault()
    const next = url.trim()
    const problem = !isHttps(next) ? text.invalidUrl : markets.includes(next) ? text.duplicate : undefined
    setInvalid(problem)
    if (problem || !settings.data) return
    save.mutate({ ...settings.data, markets: [...markets, next] }, { onSuccess: () => setUrl('') })
  }
  return (
    <div className="flex min-w-0 flex-col gap-3">
      {markets.length > 0 && (
        <ul className="grid min-w-0 gap-1.5">
          {markets.map((market) => (
            <li key={market} className="flex min-w-0 items-center gap-3 rounded-input border border-hairline bg-surface-subtle px-3 py-2 text-[13px] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-ink/[0.04]">
              <span className="min-w-0 flex-1 truncate font-mono" title={market}>
                {market}
              </span>
              <button
                type="button"
                className="shrink-0 cursor-pointer font-bold text-red-text underline outline-none focus-visible:shadow-focus disabled:opacity-40"
                aria-label={text.removeLabel(market)}
                disabled={save.isPending || !settings.data}
                onClick={() => settings.data && save.mutate({ ...settings.data, markets: markets.filter((item) => item !== market) })}
              >
                {text.remove}
              </button>
            </li>
          ))}
        </ul>
      )}
      <form className="flex min-w-0 flex-wrap items-end gap-2.5" onSubmit={submit} noValidate>
        <div className="grid min-w-0 flex-1 basis-[260px] gap-1.5">
          <Label htmlFor={`${id}-url`}>{text.url}</Label>
          <Input id={`${id}-url`} value={url} placeholder={text.urlPlaceholder} aria-invalid={invalid ? true : undefined} onChange={(event) => setUrl(event.target.value)} autoComplete="off" spellCheck={false} />
        </div>
        <Button type="submit" size="sm" disabled={save.isPending || !settings.data}>
          {text.add}
        </Button>
      </form>
      <FieldError message={invalid} />
      <ErrorAlert error={save.error ?? settings.error} />
    </div>
  )
}

function MarketSettingsDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent kind="edit" className="sm:max-w-[560px]">
        <DialogHeader>
          <DialogTitle>{text.manage}</DialogTitle>
          <DialogDescription>{text.manageHint}</DialogDescription>
        </DialogHeader>
        <MarketSettings />
      </DialogContent>
    </Dialog>
  )
}

// Unified Discover catalog combining built-in official plugins, the official market, and custom/community markets behind a compact filter
// bar and a secondary "Manage markets" dialog trigger.
export function DiscoverCatalog({
  installed,
  initialFilter = 'all',
}: {
  installed: Schemas['Plugin'][]
  initialFilter?: DiscoverFilter
}) {
  const navigate = useNavigate()
  const markets = usePluginMarkets()
  const builtin = useBuiltinPluginSources()
  const settings = usePluginSettings()
  const installer = usePluginInstaller((plugin) => void navigate(pluginPath(plugin.id)))
  const [filter, setFilter] = useState<DiscoverFilter>(initialFilter)
  const [prevInitial, setPrevInitial] = useState<DiscoverFilter>(initialFilter)
  if (prevInitial !== initialFilter) {
    setPrevInitial(initialFilter)
    setFilter(initialFilter)
  }
  const [managing, setManaging] = useState(false)

  const installedById = new Map(installed.map((plugin) => [plugin.id, plugin]))
  const allMarkets = markets.data?.markets ?? []
  const officialMarkets = allMarkets.filter((market) => market.official)
  const communityMarkets = allMarkets.filter((market) => !market.official)
  const officialIds = new Set(officialMarkets.flatMap((market) => market.plugins.map((entry) => entry.id)))
  const builtinSources = (builtin.data?.sources ?? []).filter((source) => !officialIds.has(source.id))

  const showOfficial = filter === 'all' || filter === 'official'
  const showCommunity = filter === 'all' || filter === 'community'
  const visibleBuiltin = showOfficial ? builtinSources : []
  const visibleOfficialMarkets = showOfficial ? officialMarkets : []
  const visibleCommunityMarkets = showCommunity ? communityMarkets : []
  const customMarketCount = settings.data?.markets.length ?? 0

  const isLoading = (markets.isPending && allMarkets.length === 0) && (showOfficial ? builtin.isPending : true)
  const hasVisibleContent = visibleBuiltin.length > 0 || visibleOfficialMarkets.length > 0 || visibleCommunityMarkets.length > 0

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <div className="flex flex-wrap items-center justify-between gap-2.5">
        <ToggleGroup
          aria-label={text.filterLabel}
          value={filter}
          onValueChange={(value) => value && setFilter(value as DiscoverFilter)}
          className={cn(segmentedClass, 'self-start')}
        >
          {FILTERS.map((value) => (
            <ToggleGroupItem key={value} value={value} className={segmentedItemClass}>
              {text.filters[value]}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <Button size="xs" variant="secondary" onClick={() => setManaging(true)}>
          <SlidersHorizontalIcon aria-hidden="true" className="size-3.5" />
          <span>{text.manageButton}</span>
          {customMarketCount > 0 && (
            <span className="font-mono text-[11px] text-ink-500 tabular-nums">{customMarketCount}</span>
          )}
        </Button>
      </div>

      <ErrorAlert error={markets.error ?? (showOfficial ? builtin.error : null)} />

      {isLoading ? (
        <Quiet>{text.loading}</Quiet>
      ) : !hasVisibleContent ? (
        <div className="flex flex-col items-start gap-2.5 rounded-card border border-hairline bg-surface-subtle/50 px-5 py-5">
          <Quiet>
            {filter === 'community' ? text.noOther : filter === 'official' ? text.noOfficial : text.emptyDiscover}
          </Quiet>
          {filter === 'community' && (
            <Button size="xs" variant="secondary" onClick={() => setManaging(true)}>
              {text.manageButton}
            </Button>
          )}
        </div>
      ) : (
        <div className="flex min-w-0 flex-col gap-6">
          {visibleBuiltin.length > 0 && (
            <section aria-label={t.plugins.builtinTitle} className="flex min-w-0 flex-col gap-3">
              <h2 className="riso-marker self-start text-[14px] font-semibold tracking-tight text-ink [[data-skin=plate]_&]:px-1.5 [[data-skin=plate]_&]:font-display-cn [[data-skin=plate]_&]:text-[24px] [[data-skin=plate]_&]:font-normal [[data-skin=plate]_&]:tracking-normal">
                {t.plugins.builtinTitle}
              </h2>
              <ul className="grid gap-3.5 md:grid-cols-2 xl:grid-cols-3">
                {visibleBuiltin.map((source, index) => (
                  <BuiltinCard key={source.id} source={source} installedPlugin={installedById.get(source.id)} installer={installer} index={index} />
                ))}
              </ul>
            </section>
          )}
          {visibleOfficialMarkets.map((market) => (
            <MarketSection
              key={market.url}
              market={market}
              installer={installer}
              titled={visibleBuiltin.length > 0 || visibleOfficialMarkets.length > 1 || visibleCommunityMarkets.length > 0}
            />
          ))}
          {visibleCommunityMarkets.map((market) => (
            <MarketSection key={market.url} market={market} installer={installer} titled />
          ))}
        </div>
      )}

      <MarketSettingsDialog open={managing} onOpenChange={setManaging} />
      {installer.dialog}
    </div>
  )
}
