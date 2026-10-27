import { useId } from 'react'
import type { Schemas } from '@/api/client'
import { FieldError } from '@/components/ErrorAlert'
import { Label } from '@/components/ui/label'
import { t } from '@/i18n'
import { selectClass } from '@/lib/field'

const tiers = ['default', 'priority', 'ultrafast'] satisfies Schemas['CodexServiceTier'][]

export function CodexServiceTierPicker({ value, onChange, error }: {
  value: Schemas['CodexServiceTier'] | null
  onChange: (value: Schemas['CodexServiceTier'] | null) => void
  error?: string
}) {
  const id = useId()
  const copy = t.config.basic.serviceTier
  return <div className="grid content-start gap-1.5">
    <Label htmlFor={id}>{copy.title}</Label>
    <select id={id} className={selectClass} value={value ?? 'default'} aria-describedby={`${id}-help`} aria-invalid={error ? true : undefined} onChange={event => {
      const next = tiers.find(tier => tier === event.target.value)
      if (next) onChange(next === 'default' ? null : next)
    }}>
      {tiers.map(tier => <option key={tier} value={tier}>{copy.levels[tier]}</option>)}
    </select>
    <p id={`${id}-help`} className="text-xs text-ink-500">{copy.help}</p>
    <FieldError message={error} />
  </div>
}
