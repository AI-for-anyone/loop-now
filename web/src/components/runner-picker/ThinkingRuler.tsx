import { useId } from 'react'
import { RadioGroup as RadioGroupPrimitive } from 'radix-ui'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

type ThinkingLevel = Schemas['ThinkingLevel']

// The value of the "Default" option (null in the contract).
const DEFAULT = '__default__'

interface ThinkingRulerProps {
  value: ThinkingLevel | null
  // The chosen model's levels, lowest first; empty when it has none or no model is chosen yet.
  levels: readonly ThinkingLevel[]
  defaultLevel?: ThinkingLevel | null
  // Why there is nothing to choose: no model chosen yet, or one without levels; null when the model is not in the catalog.
  hint: string | null
  onChange: (value: ThinkingLevel | null) => void
}

// Compact single-row horizontal thinking-level pill bar at the bottom of the picker. "Default" comes first with the resolved level,
// followed by the model's supported levels (highest first, matching the console's thinking scale). A radio group for keyboard navigation.
export function ThinkingRuler({ value, levels, defaultLevel, hint, onChange }: ThinkingRulerProps) {
  const labelId = useId()
  const copy = t.config.basic.thinking
  const label = (level: string) => copy.levels[level] ?? level
  const unavailable = value !== null && !levels.includes(value)
  const disabled = levels.length === 0
  const scale = [...levels].reverse()
  const marks = [
    { value: DEFAULT, text: defaultLevel ? copy.defaultWithLevel(label(defaultLevel)) : copy.default },
    ...scale.map((level) => ({ value: level, text: label(level) })),
  ]
  return (
    <div className="flex min-w-0 flex-col gap-1 border-t border-hairline bg-surface-subtle/40 px-3 py-2">
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
        <span
          id={labelId}
          className="shrink-0 text-[12px] font-medium text-ink-500"
        >
          {copy.title}
        </span>
        {disabled ? (
          hint && <p className="text-[12px] text-ink-500">{hint}</p>
        ) : (
          <RadioGroupPrimitive.Root
            aria-labelledby={labelId}
            orientation="horizontal"
            value={value === null ? DEFAULT : unavailable ? '' : value}
            onValueChange={(next) => onChange(next === DEFAULT ? null : (levels.find((level) => level === next) ?? null))}
            className="inline-flex max-w-full flex-wrap items-center gap-0.5 rounded-[8px] border border-hairline bg-segment-track p-[3px]"
          >
            {marks.map((mark) => (
              <RadioGroupPrimitive.Item
                key={mark.value}
                value={mark.value}
                className={cn(
                  'inline-flex h-6 shrink-0 cursor-pointer items-center rounded-[5px] px-2.5 text-[12px] font-medium whitespace-nowrap text-ink-500 transition-[background-color,color,box-shadow] duration-150 outline-none hover:text-ink data-[state=checked]:bg-segment-active data-[state=checked]:text-ink data-[state=checked]:shadow-[0_1px_2px_rgb(0_0_0/0.08),0_0_0_1px_var(--hairline)]',
                )}
              >
                {mark.text}
              </RadioGroupPrimitive.Item>
            ))}
          </RadioGroupPrimitive.Root>
        )}
      </div>
      {unavailable && <p className="text-[11.5px] leading-snug text-ink-500">{copy.unavailable(label(value))}</p>}
    </div>
  )
}
