import { useId } from 'react'
import { FieldError } from '@/components/ErrorAlert'
import { Label } from '@/components/ui/label'
import { t } from '@/i18n'
import { selectClass } from '@/lib/field'
import type { Schemas } from '@/api/client'

type ThinkingLevel = Schemas['ThinkingLevel']

interface ThinkingLevelPickerProps {
  value: ThinkingLevel | null
  levels: readonly ThinkingLevel[]
  defaultLevel?: ThinkingLevel | null
  onChange: (value: ThinkingLevel | null) => void
  disabled?: boolean
  error?: string
}

// Options come from the selected model's catalog, never from the label map.
export function ThinkingLevelPicker({ value, levels, defaultLevel, onChange, disabled, error }: ThinkingLevelPickerProps) {
  const id = useId()
  const copy = t.config.basic.thinking
  const label = (level: string) => copy.levels[level] ?? level
  const unavailable = value !== null && !levels.includes(value)
  return <div className="grid content-start gap-1.5">
    <Label htmlFor={id}>{copy.title}</Label>
    <select id={id} className={selectClass} value={value ?? ''} disabled={disabled} aria-invalid={error ? true : undefined} onChange={event => onChange(levels.find(level => level === event.target.value) ?? null)}>
      <option value="">{defaultLevel ? copy.defaultWithLevel(label(defaultLevel)) : copy.default}</option>
      {unavailable && <option value={value} disabled>{copy.unavailable(label(value))}</option>}
      {levels.map(level => <option key={level} value={level}>{label(level)}</option>)}
    </select>
    <FieldError message={error} />
  </div>
}
