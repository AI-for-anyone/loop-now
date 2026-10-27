import { useId, useState, type ReactNode } from 'react'
import { Combobox } from '@/components/ui/combobox'
import { Label } from '@/components/ui/label'
import type { Schemas } from '@/api/client'
import { useModelProviders, useModels } from '@/api/queries'
import { t } from '@/i18n'
import { describeError } from '@/lib/errors'
import { selectClass } from '@/lib/field'
import { FieldError } from './ErrorAlert'

interface ModelPickerProps {
  value: Schemas['ModelRef'] | null
  onChange: (value: Schemas['ModelRef'] | null) => void
  error?: string
  children?: (model: Schemas['Model'] | undefined) => ReactNode
}

// 先选提供方，再选该提供方下的模型。模型用可过滤的下拉，模型较多时可以按名称或 ID 过滤。
export function ModelPicker({ value, onChange, error, children }: ModelPickerProps) {
  const providerId = useId()
  const modelLabelId = useId()
  const providers = useModelProviders()
  const [provider, setProvider] = useState<string | null>(value?.provider ?? null)
  const models = useModels(provider)
  // 记住每个提供方最近选定的模型：切到别的提供方再切回时恢复，目录中没有的模型也能恢复。
  const [chosen, setChosen] = useState<Record<string, Schemas['ModelRef']>>(() => (value ? { [value.provider]: value } : {}))
  const [seen, setSeen] = useState(value)
  if (value !== seen) {
    setSeen(value)
    if (value) {
      setChosen((current) => ({ ...current, [value.provider]: value }))
      // 模型被外部改成别的提供方（例如表单跟随服务端刷新）时，提供方一起切换。
      if (value.provider !== provider) setProvider(value.provider)
    }
  }

  if (providers.isError) {
    return (
      <div className="grid gap-1.5">
        <CatalogUnavailable error={providers.error} value={value} />
        <FieldError message={error} />
        {children?.(undefined)}
      </div>
    )
  }

  const providerKnown = !value || !providers.data || providers.data.providers.some((p) => p.id === value.provider)
  const modelKnown = !value || !models.data || value.provider !== provider || models.data.models.some((m) => m.id === value.modelId)
  const showCurrent = value && (!providerKnown || !modelKnown)
  const options = (models.data?.models ?? []).map((m) => ({ value: m.id, label: m.name, hint: m.id }))

  return (
    <div className="grid gap-3">
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
            <option key={p.id} value={p.id}>
              {t.config.basic.providerOption(p.id, p.modelCount)}
            </option>
          ))}
        </select>
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
      {children?.(!models.isError && value?.provider === provider ? models.data?.models.find(model => model.id === value.modelId) : undefined)}
    </div>
  )
}

// 提供方列表或某个提供方的模型列表无法获取：显示原因，并保留显示当前模型。
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
