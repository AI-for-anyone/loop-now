import { useId } from 'react'
import { RadioGroup as RadioGroupPrimitive } from 'radix-ui'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'

type ThinkingLevel = Schemas['ThinkingLevel']

// The value of the "Default" mark (null in the contract).
const DEFAULT = '__default__'

interface ThinkingRulerProps {
  value: ThinkingLevel | null
  // The chosen model's levels, lowest first; empty when it has none or no model is chosen yet.
  levels: readonly ThinkingLevel[]
  defaultLevel?: ThinkingLevel | null
  hasModel: boolean
  onChange: (value: ThinkingLevel | null) => void
}

// The thinking level beside the model list, drawn as one of the console's rulers: the highest level at the top, one tick per level on the
// right edge, the chosen one under a slanted yellow marker with the pink arrow. "Default" sits above the scale and says which level it
// means. A radio group: up and down arrows move between marks. At narrow widths the picker uses the segmented ThinkingLevelPicker instead.
export function ThinkingRuler({ value, levels, defaultLevel, hasModel, onChange }: ThinkingRulerProps) {
  const labelId = useId()
  const copy = t.config.basic.thinking
  const label = (level: string) => copy.levels[level] ?? level
  const unavailable = value !== null && !levels.includes(value)
  const disabled = levels.length === 0
  const marks = [{ value: DEFAULT, text: defaultLevel ? copy.defaultWithLevel(label(defaultLevel)) : copy.default, tick: false }, ...[...levels].reverse().map((level) => ({ value: level, text: label(level), tick: true }))]
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-2 border-l-2 border-dashed border-ink/35 px-2.5 pt-3 pb-3">
      <span id={labelId} className="px-1 text-[12.5px] leading-tight font-black">{copy.title}</span>
      {disabled && <p className="px-1 text-[12px] leading-snug font-bold text-ink-500">{hasModel ? t.runtime.picker.noThinking : t.runtime.picker.noModel}</p>}
      <RadioGroupPrimitive.Root
        aria-labelledby={labelId}
        orientation="vertical"
        disabled={disabled}
        value={value === null ? DEFAULT : unavailable ? '' : value}
        onValueChange={(next) => onChange(next === DEFAULT ? null : (levels.find((level) => level === next) ?? null))}
        className={cn('relative flex flex-col', disabled && 'opacity-40')}
      >
        {/* The ruler's spine on the right edge. */}
        <span aria-hidden="true" className="absolute top-[38px] right-0 bottom-3 w-0.5 bg-ink/70" />
        {marks.map((mark, index) => (
          <RadioGroupPrimitive.Item
            key={mark.value}
            value={mark.value}
            className={cn(
              'group relative flex h-[30px] cursor-pointer items-center pr-4 pl-3 text-left text-[13px] font-black whitespace-nowrap text-ink-700 outline-none hover:text-ink focus-visible:shadow-focus data-[state=checked]:text-ink',
              index === 0 && 'mb-2 border-b-2 border-dashed border-ink/25 pb-0.5',
            )}
          >
            {/* The chosen mark: a slanted yellow marker over the text, with the pink arrow on its left. */}
            <span aria-hidden="true" className="absolute inset-y-1 right-2.5 left-1.5 -rotate-[1.5deg] bg-riso-yellow opacity-0 mix-blend-multiply group-data-[state=checked]:opacity-100" />
            <span aria-hidden="true" className="absolute top-1/2 -left-1 size-0 -translate-y-1/2 border-y-[5px] border-l-[7px] border-y-transparent border-l-riso-pink opacity-0 group-data-[state=checked]:opacity-100" />
            <span className="relative">{mark.text}</span>
            {mark.tick && <span aria-hidden="true" className="absolute top-1/2 right-0 h-0.5 w-2.5 bg-ink/70 group-data-[state=checked]:w-3.5 group-data-[state=checked]:bg-ink" />}
          </RadioGroupPrimitive.Item>
        ))}
      </RadioGroupPrimitive.Root>
      {unavailable && <p className="px-1 text-[12px] leading-snug text-ink-500">{copy.unavailable(label(value))}</p>}
    </div>
  )
}
