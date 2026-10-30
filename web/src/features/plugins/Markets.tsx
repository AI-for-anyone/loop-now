import { useId, useState, type FormEvent } from 'react'
import { Link, useNavigate } from 'react-router'
import type { Schemas } from '@/api/client'
import { marketSource, usePluginMarkets, usePluginSettings, useSavePluginSettings } from '@/api/plugins'
import { ErrorAlert, FieldError } from '@/components/ErrorAlert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { t } from '@/i18n'
import { localizedText } from '@/lib/localized-text'
import { cn } from '@/lib/utils'
import { cardClass, Quiet, SourceBadge } from './parts'
import { pluginPath } from './paths'
import { usePluginInstaller } from './usePluginInstaller'

type Market = Schemas['PluginMarket']
type Entry = Schemas['PluginMarketEntry']
const text = t.plugins.markets

// 市场里的一个插件：名称、官方或已验证、说明、版本；没装的可安装，装了的写已装版本，有新版本时可更新。
function EntryCard({ market, entry, installer }: { market: Market; entry: Entry; installer: ReturnType<typeof usePluginInstaller> }) {
  const key = `${market.url}#${entry.id}`
  const busy = installer.pending !== null
  return (
    <li className={cn(cardClass, 'flex min-w-0 flex-col gap-2 px-4 py-3.5')}>
      <div className="flex min-w-0 flex-wrap items-center gap-2">
        <h3 className="font-display-cn text-[19px] leading-tight font-normal [overflow-wrap:anywhere]">
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
          <span className={cn('rounded-[6px] px-1.5 py-px text-[11.5px] leading-4 font-black', entry.verified ? 'bg-tint-green text-green-text' : 'bg-tint-yellow text-ink')}>
            {entry.verified ? text.verified : text.unverified}
          </span>
        )}
      </div>
      <p className="text-[13px] leading-snug text-ink-700 [overflow-wrap:anywhere]">{localizedText(entry.description)}</p>
      <p className="truncate font-mono text-[11.5px] text-ink-500" title={entry.source}>
        {entry.source}
      </p>
      <div className="mt-auto flex flex-wrap items-center justify-between gap-2 pt-1">
        <span className="font-mono text-[12px] text-ink-500">{entry.installedVersion ? text.installed(entry.installedVersion) : entry.version ? `v${entry.version}` : null}</span>
        {!entry.installedVersion ? (
          <Button size="xs" variant="secondary" disabled={busy} onClick={() => void installer.begin(key, { source: marketSource(market.url, entry) })}>
            {installer.pending === key ? t.plugins.installEntry.reading : t.plugins.install}
          </Button>
        ) : entry.updateAvailable ? (
          <Button size="xs" disabled={busy} onClick={() => void installer.begin(key, { source: marketSource(market.url, entry) })}>
            {installer.pending === key ? t.plugins.installEntry.reading : text.updateTo(entry.version)}
          </Button>
        ) : (
          <span className="text-[12.5px] font-bold text-green-text">{text.upToDate}</span>
        )}
      </div>
      <ErrorAlert error={installer.errorFor(key)} />
    </li>
  )
}

function MarketSection({ market, installer, titled }: { market: Market; installer: ReturnType<typeof usePluginInstaller>; titled: boolean }) {
  const name = market.name ?? text.unnamed
  return (
    <section aria-label={name} className="flex min-w-0 flex-col gap-2.5">
      {titled && (
        <h2 className="flex min-w-0 flex-wrap items-baseline gap-x-2 font-display-cn text-[19px] font-normal">
          {name}
          <span className="min-w-0 truncate font-sans text-[12px] font-bold text-ink-500">{market.url}</span>
        </h2>
      )}
      {market.error ? (
        <p role="alert" className="rounded-input bg-tint-red px-3.5 py-2.5 text-[13px] font-bold text-red-text [overflow-wrap:anywhere]">
          {text.failed(name, market.error)}
        </p>
      ) : market.plugins.length === 0 ? (
        <Quiet>{text.empty}</Quiet>
      ) : (
        <ul className="grid gap-3.5 md:grid-cols-2 xl:grid-cols-3">
          {market.plugins.map((entry) => (
            <EntryCard key={entry.id} market={market} entry={entry} installer={installer} />
          ))}
        </ul>
      )}
    </section>
  )
}

// 官方市场或其他市场的插件；安装或更新后打开插件详情。
export function MarketPlugins({ official }: { official: boolean }) {
  const navigate = useNavigate()
  const markets = usePluginMarkets()
  const installer = usePluginInstaller((plugin) => void navigate(pluginPath(plugin.id)))
  const list = (markets.data?.markets ?? []).filter((market) => market.official === official)
  return (
    <div className="flex min-w-0 flex-col gap-5">
      {!official && <MarketSettings />}
      <ErrorAlert error={markets.error} />
      {markets.isPending ? (
        <Quiet>{text.loading}</Quiet>
      ) : list.length === 0 ? (
        markets.isSuccess && <Quiet>{official ? text.noOfficial : text.noOther}</Quiet>
      ) : (
        list.map((market) => <MarketSection key={market.url} market={market} installer={installer} titled={!official || list.length > 1} />)
      )}
      {installer.dialog}
    </div>
  )
}

const isHttps = (value: string) => {
  try {
    return new URL(value).protocol === 'https:'
  } catch {
    return false
  }
}

// 其他市场的地址：保存在全局插件设置的 markets 里（与一键停用一起保存）。
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
    <section aria-label={text.manage} className={cn(cardClass, 'flex min-w-0 flex-col gap-3 px-4 py-3.5')}>
      <h2 className="font-display-cn text-[19px] font-normal">{text.manage}</h2>
      <p className="text-[12.5px] text-ink-700">{text.manageHint}</p>
      {markets.length > 0 && (
        <ul className="grid min-w-0 gap-1.5">
          {markets.map((market) => (
            <li key={market} className="flex min-w-0 items-center gap-3 rounded-input bg-ink/[0.04] px-3 py-2 text-[13px]">
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
    </section>
  )
}
