import type { Schemas } from '@/api/client'
import { Badge } from '@/components/ui/badge'
import { t } from '@/i18n'

const text = t.plugins.providers
type Detail = Schemas['PluginDetail']
type Provider = Schemas['PluginModelProvider']

// Why it isn't in the model catalog: only providers that have OAuth, have an id different from both the built-in ones and models.json,
// and registered without errors get in.
function outside(provider: Provider): string | null {
  if (provider.inCatalog) return null
  if (provider.error !== null) return text.error(provider.error)
  if (provider.overridesBuiltin) return text.overridesBuiltin
  if (!provider.oauth) return text.noOAuth
  return text.notInCatalog
}

// The model providers registered by the current version's Pi extensions, read when installing or updating; read-only. Names and errors
// are the extension's own text, shown as plain text.
export function PluginProviders({ detail }: { detail: Detail }) {
  const { providers, providerErrors, providersError, providerState } = detail
  return (
    <div className="grid min-w-0 gap-2.5">
      {providersError !== null && (
        <p role="alert" className="text-[13px] leading-snug font-bold whitespace-pre-wrap text-red-text [overflow-wrap:anywhere]">
          {text.failed(providersError)}
        </p>
      )}
      {providers.length > 0 ? (
        <ul className="grid min-w-0 gap-1.5">
          {providers.map((provider) => {
            const reason = outside(provider)
            const paths = providerState[provider.id]?.paths
            return (
              <li key={provider.id} className="grid min-w-0 gap-1 rounded-input border border-hairline bg-surface-subtle px-3 py-2 text-[13px] leading-snug [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-ink/[0.04]">
                <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                  <span className="font-semibold text-ink [overflow-wrap:anywhere] [[data-skin=plate]_&]:font-black">{provider.name || provider.id}</span>
                  {provider.name !== provider.id && <code className="font-mono text-[12px] text-ink-500 [overflow-wrap:anywhere]">{provider.id}</code>}
                  {provider.oauth && <Badge tone="success">{text.oauth}</Badge>}
                  {provider.inCatalog && <Badge tone="info">{text.inCatalog}</Badge>}
                </span>
                {reason && <span className="whitespace-pre-wrap text-red-text [overflow-wrap:anywhere]">{reason}</span>}
                {paths ? (
                  <span className="text-ink-700 [overflow-wrap:anywhere]">
                    {text.statePaths}
                    <code className="font-mono text-[12px]">{t.plugins.confirm.list(paths)}</code>
                  </span>
                ) : (
                  provider.inCatalog && <span className="font-bold text-ink [overflow-wrap:anywhere]">{text.noStatePaths}</span>
                )}
                <span className="text-ink-700 [overflow-wrap:anywhere]">
                  {text.models(provider.modelCount)} · {text.extension(provider.extension)}
                </span>
              </li>
            )
          })}
        </ul>
      ) : (
        providersError === null && <p className="text-[13px] text-ink-700">{text.none}</p>
      )}
      {providerErrors.length > 0 && (
        <section className="grid min-w-0 gap-1.5">
          <h3 className="text-[13px] font-semibold text-ink-700 [[data-skin=plate]_&]:font-black">{text.loadErrors}</h3>
          <ul className="grid min-w-0 gap-1.5">
            {providerErrors.map((item) => (
              <li key={item.path} className="grid min-w-0 gap-0.5 rounded-input bg-tint-red px-3 py-2 text-[13px] leading-snug">
                <code className="font-mono text-[12px] font-bold [overflow-wrap:anywhere]">{item.path}</code>
                <span className="whitespace-pre-wrap text-red-text [overflow-wrap:anywhere]">{item.error}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  )
}
