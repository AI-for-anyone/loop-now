import { useId } from 'react'
import { RadioGroup as RadioGroupPrimitive } from 'radix-ui'
import { FieldError } from '@/components/ErrorAlert'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
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

// The value of the “默认” (Default) segment in the radio group (null in the contract).
const DEFAULT = '__default__'

// Thinking level: a set of segmented buttons (2px navy border, 12px corner radius, the selected segment yellow) with radio group semantics;
// the left and right arrow keys move between segments.
// The first segment is “默认” (Default) (it states which level the default is); the other segments only take the levels in the selected
// model's catalog. When the stored level is not in the catalog, no segment is selected and a note below says the current value is not
// available for now.
export function ThinkingLevelPicker({ value, levels, defaultLevel, onChange, disabled, error }: ThinkingLevelPickerProps) {
  const labelId = useId()
  const copy = t.config.basic.thinking
  const label = (level: string) => copy.levels[level] ?? level
  const unavailable = value !== null && !levels.includes(value)
  const options = [{ value: DEFAULT, text: defaultLevel ? copy.defaultWithLevel(label(defaultLevel)) : copy.default }, ...levels.map((level) => ({ value: level, text: label(level) }))]
  return (
    <div className="grid min-w-0 content-start gap-1.5">
      <span id={labelId} className="text-[13.5px] leading-snug font-black">
        {copy.title}
      </span>
      <RadioGroupPrimitive.Root
        aria-labelledby={labelId}
        aria-invalid={error ? true : undefined}
        orientation="horizontal"
        disabled={disabled}
        value={value === null ? DEFAULT : unavailable ? '' : value}
        onValueChange={(next) => onChange(next === DEFAULT ? null : (levels.find((level) => level === next) ?? null))}
        className="flex h-11 min-w-0 overflow-hidden rounded-input border-2 border-ink bg-white aria-invalid:border-red"
      >
        {options.map((option, index) => (
          <RadioGroupPrimitive.Item
            key={option.value}
            value={option.value}
            // The “默认” (Default) segment is as wide as its text (it states which level the default is), and the levels share the
            // remaining width equally.
            className={cn(
              'cursor-pointer truncate text-[13.5px] font-black whitespace-nowrap text-ink-700 outline-none hover:text-ink focus-visible:shadow-[inset_0_0_0_3px_var(--riso-yellow),inset_0_0_0_5px_var(--ink)] disabled:cursor-not-allowed disabled:opacity-40 data-[state=checked]:bg-riso-yellow data-[state=checked]:text-ink',
              index === 0 ? 'shrink-0 px-3' : 'min-w-0 flex-1 border-l-2 border-ink px-1.5',
            )}
          >
            {option.text}
          </RadioGroupPrimitive.Item>
        ))}
      </RadioGroupPrimitive.Root>
      {unavailable && <p className="text-[12.5px] text-ink-500">{copy.unavailable(label(value))}</p>}
      <FieldError message={error} />
    </div>
  )
}
