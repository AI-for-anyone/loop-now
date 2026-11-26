import { t } from '@/i18n'
import { useId } from 'react'
import { Link } from 'react-router'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { selectClass } from '@/lib/field'

// A presentation-only input. The status explanation is supplied by the caller from the authoritative resolution result; it is not
// re-resolved here from candidate availability.
export function AccountSelectionField({ label, description, options, value, currentLabel, canRestore, restoreLabel = t.credentials.selectionField.restoreAutomatic, disabled, pending, dirty, embedded = false, settingsPath, onChange, onSave, onRestore }: {
  label: string
  description: string
  options: { value: string; label: string }[]
  value: string
  currentLabel?: string
  canRestore: boolean
  restoreLabel?: string
  disabled?: boolean
  pending?: boolean
  dirty?: boolean
  embedded?: boolean
  settingsPath: string
  onChange: (value: string) => void
  onSave: () => void
  onRestore: () => void
}) {
  const id = useId()
  return (
    <div
      className={
        embedded
          ? 'grid min-w-0 gap-2.5 text-[13px]'
          : 'grid min-w-0 gap-2.5 rounded-card border border-hairline bg-surface-subtle p-3.5 text-[13px] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue'
      }
    >
      <div className="grid gap-0.5">
        <Label htmlFor={id}>{label}</Label>
        <p id={`${id}-hint`} className="text-[12.5px] text-ink-700 [overflow-wrap:anywhere]">{description}</p>
      </div>
      <select id={id} className={selectClass} value={value} aria-describedby={`${id}-hint`} disabled={disabled || pending || options.length === 0} onChange={e => onChange(e.target.value)}>
        <option value="">{currentLabel ?? t.credentials.selectionField.choosePlaceholder}</option>
        {value && !options.some(option => option.value === value) && <option value={value}>{t.credentials.selectionField.selectedUnavailable}</option>}
        {options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" size="sm" disabled={disabled || pending || !dirty || !value} onClick={onSave}>{pending ? t.credentials.selectionField.saving : t.credentials.selectionField.save}</Button>
        {canRestore && <Button type="button" size="sm" variant="secondary" disabled={disabled || pending} onClick={onRestore}>{restoreLabel}</Button>}
        <Link to={settingsPath} className="inline-flex min-h-8 items-center text-[12.5px] font-medium underline underline-offset-4 [[data-skin=plate]_&]:font-bold">{t.credentials.selectionField.manageInSettings}</Link>
      </div>
    </div>
  )
}
