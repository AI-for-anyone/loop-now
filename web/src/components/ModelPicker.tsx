import { useId, useState, type ReactNode } from 'react'
import { Combobox } from '@/components/ui/combobox'
import { Label } from '@/components/ui/label'
import type { Schemas } from '@/api/client'
import { useModelProviders, useModels, useRuntimes } from '@/api/queries'
import { t } from '@/i18n'
import { describeError } from '@/lib/errors'
import { localizedText } from '@/lib/localized-text'
import { selectClass } from '@/lib/field'
import { runtimeProblem } from '@/lib/runtime'
import { FieldError } from './ErrorAlert'

interface ModelPickerProps {
  value: Schemas['ModelRef'] | null
  onChange: (value: Schemas['ModelRef'] | null) => void
  error?: string
  // The selected model and provider (when they are in the catalog): for settings that follow the model, such as the thinking level.
  children?: (model: Schemas['Model'] | undefined, provider: Schemas['ModelProvider'] | undefined) => ReactNode
  // The Agent being edited: the catalog is read with it, so plugin providers show why this Agent cannot use them right now. Not given when
  // creating an Agent, and then plugin providers cannot be picked directly.
  agentId?: string
}

type Provider = Schemas['ModelProvider']
type Unavailable = 'create' | NonNullable<Provider['unavailableReason']>

// Why a plugin provider cannot be used by this Agent right now; when creating an Agent, it always has to wait until after creation and
// grant. plugins_disabled does not block saving.
const unavailableOf = (provider: Provider, agentId: string | undefined): Unavailable | null =>
  provider.source !== 'plugin' ? null : agentId === undefined ? 'create' : provider.unavailableReason
const blocks = (reason: Unavailable | null) => reason !== null && reason !== 'plugins_disabled'
const pluginName = (provider: Provider) => (provider.pluginDisplayName === null ? provider.pluginId ?? '' : localizedText(provider.pluginDisplayName) || provider.pluginId || '')

function providerOption(provider: Provider, agentId: string | undefined) {
  const basic = t.config.basic
  const label =
    provider.source === 'plugin' ? basic.pluginProviderOption(provider.name, provider.id, pluginName(provider), provider.modelCount)
      : provider.source === 'local_cli' ? t.runtime.providerOption(provider.name || provider.id, provider.modelCount)
        : basic.providerOption(provider.id, provider.modelCount)
  const reason = unavailableOf(provider, agentId)
  return reason ? basic.unavailableOption(label, basic.pluginUnavailable[reason]) : label
}

// The selected plugin provider cannot be used by this Agent right now: explain why and where to fix it.
function unavailableNote(provider: Provider, agentId: string | undefined): string | null {
  const reason = unavailableOf(provider, agentId), note = t.config.basic.pluginUnavailableNote
  if (reason === null || reason === 'create') return null
  if (reason === 'plugin_not_enabled' || reason === 'agent_not_granted') return note[reason](pluginName(provider))
  return note[reason]
}

// Pick the provider first, then a model of that provider. Models use a filterable dropdown, so with many models you can filter by name
// or ID.
// When a plugin provider cannot be picked right now, its option carries the reason and is disabled (all plugins disabled globally only
// shows a hint and does not disable it).
// With the local Claude Code picked: explain that it uses this machine's installation and login and needs no account; add a warning when
// it is not installed properly or not logged in on this machine (it can still be picked).
export function ModelPicker({ value, onChange, error, children, agentId }: ModelPickerProps) {
  const providerId = useId()
  const modelLabelId = useId()
  const providers = useModelProviders(agentId)
  const [provider, setProvider] = useState<string | null>(value?.provider ?? null)
  const models = useModels(provider)
  const claudeCode = providers.data?.providers.find((p) => p.id === provider)?.runtime === 'claude-code'
  const runtimes = useRuntimes(claudeCode)
  // Remember the model last picked for each provider: it is restored when switching to another provider and back, even a model that is
  // not in the catalog.
  const [chosen, setChosen] = useState<Record<string, Schemas['ModelRef']>>(() => (value ? { [value.provider]: value } : {}))
  const [seen, setSeen] = useState(value)
  if (value !== seen) {
    setSeen(value)
    if (value) {
      setChosen((current) => ({ ...current, [value.provider]: value }))
      // When the model is changed from outside to another provider (e.g. the form follows a server refresh), switch the provider along with it.
      if (value.provider !== provider) setProvider(value.provider)
    }
  }

  if (providers.isError) {
    return (
      <div className="grid gap-1.5">
        <CatalogUnavailable error={providers.error} value={value} />
        <FieldError message={error} />
        {children?.(undefined, undefined)}
      </div>
    )
  }

  const providerKnown = !value || !providers.data || providers.data.providers.some((p) => p.id === value.provider)
  const modelKnown = !value || !models.data || value.provider !== provider || models.data.models.some((m) => m.id === value.modelId)
  const showCurrent = value && (!providerKnown || !modelKnown)
  const options = (models.data?.models ?? []).map((m) => ({ value: m.id, label: m.name, hint: m.id }))
  const current = providers.data?.providers.find((p) => p.id === provider)
  const note = current ? unavailableNote(current, agentId) : null

  // In the two-column layout (Agent config page), fields in the same row align to the top, so the note under the provider does not push
  // the model next to it down.
  return (
    <div className="grid items-start gap-3">
      <div className="grid gap-1.5">
        <Label htmlFor={providerId}>{t.config.basic.provider}</Label>
        <select
          id={providerId}
          className={selectClass}
          value={provider ?? ''}
          disabled={!providers.data}
          onChange={(e) => {
            const next = e.target.value || null
            setProvider(next)
            onChange(next ? (value?.provider === next ? value : (chosen[next] ?? null)) : null)
          }}
        >
          <option value="">{t.config.basic.providerPlaceholder}</option>
          {providers.data?.providers.map((p) => (
            <option key={p.id} value={p.id} disabled={blocks(unavailableOf(p, agentId))}>
              {providerOption(p, agentId)}
            </option>
          ))}
        </select>
        {note && <p role="note" className="text-[13.5px] font-bold text-ink-700">{note}</p>}
        {current?.runtime === 'claude-code' && <p role="note" className="text-[13.5px] text-ink-700">{t.runtime.providerNote}</p>}
        {claudeCode && <RuntimeWarning status={runtimes.data?.runtimes.find((runtime) => runtime.id === 'claude-code')} />}
      </div>
      {provider && models.isError && <CatalogUnavailable error={models.error} value={value?.provider === provider ? value : null} />}
      {provider && !models.isError && (
        <div className="grid gap-1.5">
          <Label id={modelLabelId}>{t.config.basic.model}</Label>
          <Combobox
            aria-labelledby={modelLabelId}
            value={value?.provider === provider ? value.modelId : null}
            options={options}
            onChange={(modelId) => onChange({ provider, modelId })}
            placeholder={t.config.basic.modelPlaceholder}
            filterLabel={t.config.basic.modelFilter}
            emptyText={t.config.basic.modelEmpty}
            describe={(option) => t.config.basic.modelOption(option.label, option.value)}
            invalid={Boolean(error)}
            disabled={!models.data}
          />
        </div>
      )}
      {showCurrent && (
        <p className="text-[13.5px] font-bold text-ink-700" role="note">
          {t.config.basic.notInCatalog}
          <span className="ml-1 font-mono">{`${value.provider} / ${value.modelId}`}</span>
        </p>
      )}
      <FieldError message={error} />
      {children?.(!models.isError && value?.provider === provider ? models.data?.models.find(model => model.id === value.modelId) : undefined, current)}
    </div>
  )
}

// The local Claude Code cannot be used right now (not found, version unreadable, not logged in): the same explanation as in global
// settings, plus a note that runs will fail until it is fixed.
// No warning when the runtime status cannot be read or the login state cannot be determined.
function RuntimeWarning({ status }: { status: Schemas['RuntimeStatus'] | undefined }) {
  const problem = runtimeProblem(status)
  if (!problem) return null
  return <p role="note" className="rounded-input bg-tint-yellow px-3 py-2 text-[13.5px] font-bold text-ink">{t.runtime.status.pickerWarning(t.runtime.status.problems[problem])}</p>
}

// The provider list or a provider's model list cannot be fetched: show the reason, and keep showing the current model.
function CatalogUnavailable({ error, value }: { error: unknown; value: Schemas['ModelRef'] | null }) {
  return (
    <div className="grid gap-1.5">
      <p className="text-[13.5px] font-bold text-red-text">{t.config.basic.catalogUnavailable}</p>
      <p className="text-[13.5px] text-red-text">{t.config.basic.catalogReason(describeError(error))}</p>
      {value && (
        <p className="text-[13.5px]">
          {t.config.basic.currentModel}
          <span className="ml-1 font-mono">{`${value.provider} / ${value.modelId}`}</span>
        </p>
      )}
    </div>
  )
}
