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
    <div className="flex min-w-0 flex-col gap-1 border-t border-hairline bg-surface-subtle/40 px-3 py-2 [[data-skin=plate]_&]:border-t-2 [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/35 [[data-skin=plate]_&]:bg-paper-tab/60">
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-2">
        <span
          id={labelId}
          className="shrink-0 text-[12px] font-medium text-ink-500 [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink"
        >
          {copy.title}
        </span>
        {disabled ? (
          hint && <p className="text-[12px] text-ink-500 [[data-skin=plate]_&]:font-bold">{hint}</p>
        ) : (
          <RadioGroupPrimitive.Root
            aria-labelledby={labelId}
            orientation="horizontal"
            value={value === null ? DEFAULT : unavailable ? '' : value}
            onValueChange={(next) => onChange(next === DEFAULT ? null : (levels.find((level) => level === next) ?? null))}
            className="inline-flex max-w-full flex-wrap items-center gap-0.5 rounded-input border border-hairline bg-surface-hover/70 p-0.5 [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised"
          >
            {marks.map((mark) => (
              <RadioGroupPrimitive.Item
                key={mark.value}
                value={mark.value}
                className={cn(
                  'inline-flex h-6.5 shrink-0 cursor-pointer items-center rounded-ticket px-2.5 text-[12px] font-medium whitespace-nowrap text-ink-500 transition-colors outline-none hover:text-ink data-[state=checked]:bg-surface-elevated data-[state=checked]:font-semibold data-[state=checked]:text-ink data-[state=checked]:shadow-xs [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-ink-700 [[data-skin=plate]_&]:focus-visible:shadow-focus [[data-skin=plate]_&]:data-[state=checked]:bg-riso-yellow [[data-skin=plate]_&]:data-[state=checked]:font-black [[data-skin=plate]_&]:data-[state=checked]:text-ink [[data-skin=plate]_&]:data-[state=checked]:shadow-[inset_0_0_0_1.5px_var(--ink)]',
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
