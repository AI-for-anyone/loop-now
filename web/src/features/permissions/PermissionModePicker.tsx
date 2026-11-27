import { FilePenLineIcon, HandIcon, ScanEyeIcon, ZapIcon, type LucideIcon } from 'lucide-react'
import { useId } from 'react'
import type { Schemas } from '@/api/client'
import { RadioCard, RadioCards } from '@/components/ui/radio-cards'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { t } from '@/i18n'
import { asksByOwnRules, effectiveMode, PERMISSION_MODES } from '@/lib/permissions'
import { cn } from '@/lib/utils'

type Mode = Schemas['PermissionMode']

const MODES: readonly Mode[] = PERMISSION_MODES
const ICONS: Record<Mode, LucideIcon> = { supervised: HandIcon, accept_edits: FilePenLineIcon, auto: ScanEyeIcon, full: ZapIcon }

export function PermissionModeIcon({ mode, className }: { mode: Mode; className?: string }) {
  const Icon = ICONS[mode]
  return <Icon aria-hidden="true" className={className} strokeWidth={2.6} />
}

// What the chosen mode means on this runtime: Pi runs only on Full access; a runtime without the mode runs it as Supervised; one that
// asks by its own rules lets some actions through without asking.
function RuntimeNote({ mode, runtime, className }: { mode: Mode; runtime?: Pick<Schemas['RuntimeDefinition'], 'name' | 'capabilities'>; className?: string }) {
  if (!runtime) return null
  const modes = runtime.capabilities.permissionModes
  const lines = [
    !modes.includes('supervised') ? t.permissions.onlyFull(runtime.name) : effectiveMode(mode, runtime) !== mode ? t.permissions.fallback(runtime.name, t.permissions.modes[mode].name) : null,
    asksByOwnRules(mode, runtime) ? t.permissions.ownRules(runtime.name) : null,
  ].filter((line) => line !== null)
  return lines.map((line) => (
    <p key={line} role="note" className={cn('text-[12.5px] font-medium text-ink-700', className)}>
      {line}
    </p>
  ))
}

// The four modes (as T3), most careful first. cards: on the config page and in Settings, each with what it means; compact: a row of
// pills under the model picker in the message box. runtime: the Agent's runtime, which may lack a mode (a runtime with only Full access
// offers nothing else); omitted for the default of new Agents.
export function PermissionModePicker({
  value,
  onChange,
  runtime,
  variant = 'cards',
  disabled,
  labelledBy,
}: {
  value: Mode
  onChange: (mode: Mode) => void
  runtime?: Pick<Schemas['RuntimeDefinition'], 'name' | 'capabilities'>
  variant?: 'cards' | 'compact'
  disabled?: boolean
  labelledBy?: string
}) {
  const hintId = useId()
  const onlyFull = runtime !== undefined && !runtime.capabilities.permissionModes.includes('supervised')
  if (variant === 'compact') {
    return (
      <div className="flex min-w-0 flex-col gap-1">
        <ToggleGroup
          aria-labelledby={labelledBy}
          aria-describedby={hintId}
          // Compact Vercel Geist segmented track that wraps cleanly on narrow screens.
          className="w-fit max-w-full justify-start p-[3px]"
          value={value}
          disabled={disabled}
          onValueChange={(next) => next && onChange(next as Mode)}
        >
          {MODES.map((mode) => (
            <ToggleGroupItem
              key={mode}
              value={mode}
              disabled={onlyFull && mode !== 'full'}
              className="h-6.5 min-h-0 gap-1 px-2 text-[11.5px]"
            >
              <PermissionModeIcon mode={mode} className="size-3" />
              {t.permissions.modes[mode].name}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <p id={hintId} className="text-[11.5px] leading-snug text-ink-500">
          {t.permissions.modes[value].hint}
        </p>
        <RuntimeNote mode={value} runtime={runtime} className="text-[11.5px] leading-snug text-ink-500" />
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-2.5">
      <RadioCards aria-labelledby={labelledBy} value={value} disabled={disabled} onValueChange={(next) => onChange(next as Mode)} className="sm:grid-cols-2">
        {MODES.map((mode) => (
          <RadioCard
            key={mode}
            value={mode}
            disabled={onlyFull && mode !== 'full'}
            data-mode={mode}
            title={
              <span className="inline-flex items-center gap-1.5">
                <PermissionModeIcon mode={mode} className={cn('size-4', mode === 'full' ? 'text-pink-text' : 'text-riso-blue')} />
                {t.permissions.modes[mode].name}
              </span>
            }
            description={t.permissions.modes[mode].hint}
          />
        ))}
      </RadioCards>
      <RuntimeNote mode={value} runtime={runtime} />
    </div>
  )
}
