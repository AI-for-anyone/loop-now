import { t } from '@/i18n'
import { useId } from 'react'
import { Link } from 'react-router'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { selectClass } from '@/lib/field'

// 纯展示输入。状态说明由正式解析结果的调用方给出，不在此按候选可用性重新解析。
export function AccountSelectionField({ label, description, options, value, currentLabel, canRestore, restoreLabel = t.credentials.selectionField.restoreAutomatic, disabled, pending, dirty, settingsPath, onChange, onSave, onRestore }: {
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
  settingsPath: string
  onChange: (value: string) => void
  onSave: () => void
  onRestore: () => void
}) {
  const id = useId()
  return <div className="grid min-w-0 gap-3 rounded-card bg-tint-blue p-4 text-[13.5px]">
    <Label htmlFor={id}>{label}</Label>
    <p id={`${id}-hint`} className="[overflow-wrap:anywhere]">{description}</p>
    <select id={id} className={selectClass} value={value} aria-describedby={`${id}-hint`} disabled={disabled || pending || options.length === 0} onChange={e => onChange(e.target.value)}>
      <option value="">{currentLabel ?? t.credentials.selectionField.choosePlaceholder}</option>
      {value && !options.some(option => option.value === value) && <option value={value}>{t.credentials.selectionField.selectedUnavailable}</option>}
      {options.map(option => <option key={option.value} value={option.value}>{option.label}</option>)}
    </select>
    <div className="flex flex-wrap items-center gap-2">
      <Button type="button" size="sm" disabled={disabled || pending || !dirty || !value} onClick={onSave}>{pending ? t.credentials.selectionField.saving : t.credentials.selectionField.save}</Button>
      {canRestore && <Button type="button" size="sm" variant="secondary" disabled={disabled || pending} onClick={onRestore}>{restoreLabel}</Button>}
      <Link to={settingsPath} className="inline-flex min-h-9 items-center font-bold underline underline-offset-4">{t.credentials.selectionField.manageInSettings}</Link>
    </div>
    <p className="text-ink-700">{t.credentials.selectionField.effectNote}</p>
  </div>
}
