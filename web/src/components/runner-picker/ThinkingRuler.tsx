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
  // vertical: a column beside the models, the highest level at the top. horizontal: a row under them at narrow widths, lowest first, that
  // scrolls sideways rather than cutting labels short.
  orientation?: 'vertical' | 'horizontal'
}

// The thinking level drawn as one of the console's rulers: one tick per level on the edge, the chosen one under a slanted yellow marker with
// the pink arrow. "Default" comes first and says which level it means. A radio group: the arrow keys move between marks.
export function ThinkingRuler({ value, levels, defaultLevel, hasModel, onChange, orientation = 'vertical' }: ThinkingRulerProps) {
  const labelId = useId()
  const copy = t.config.basic.thinking
  const label = (level: string) => copy.levels[level] ?? level
  const unavailable = value !== null && !levels.includes(value)
  const disabled = levels.length === 0
  const vertical = orientation === 'vertical'
  const scale = vertical ? [...levels].reverse() : levels
  const marks = [{ value: DEFAULT, text: defaultLevel ? copy.defaultWithLevel(label(defaultLevel)) : copy.default, tick: false }, ...scale.map((level) => ({ value: level, text: label(level), tick: true }))]
  return (
    <div className={cn('flex min-w-0 flex-col gap-2', vertical ? 'flex-1 border-l-2 border-dashed border-ink/35 px-2.5 pt-3 pb-3' : 'px-3 pt-2.5 pb-3')}>
      <span id={labelId} className="px-1 text-[12.5px] leading-tight font-black">{copy.title}</span>
      {disabled && <p className="px-1 text-[12px] leading-snug font-bold text-ink-500">{hasModel ? t.runtime.picker.noThinking : t.runtime.picker.noModel}</p>}
      <RadioGroupPrimitive.Root
        aria-labelledby={labelId}
        orientation={orientation}
        disabled={disabled}
        value={value === null ? DEFAULT : unavailable ? '' : value}
        onValueChange={(next) => onChange(next === DEFAULT ? null : (levels.find((level) => level === next) ?? null))}
        className={cn('relative flex', vertical ? 'flex-col' : 'no-scrollbar -mx-3 overflow-x-auto px-3 pt-2 pb-1', disabled && 'opacity-40')}
      >
        {/* The ruler's spine: on the right edge of the column, or along the bottom of the row. */}
        <span aria-hidden="true" className={cn('absolute bg-ink/70', vertical ? 'top-[38px] right-0 bottom-3 w-0.5' : 'right-3 bottom-1 left-3 h-0.5')} />
        {marks.map((mark, index) => (
          <RadioGroupPrimitive.Item
            key={mark.value}
            value={mark.value}
            className={cn(
              'group relative flex shrink-0 cursor-pointer items-center text-left text-[13px] font-black whitespace-nowrap text-ink-700 outline-none hover:text-ink focus-visible:shadow-focus data-[state=checked]:text-ink',
              vertical ? 'h-[30px] pr-4 pl-3' : 'h-[34px] px-3 pb-1.5',
              vertical && index === 0 && 'mb-2 border-b-2 border-dashed border-ink/25 pb-0.5',
              !vertical && index === 0 && 'mr-2 border-r-2 border-dashed border-ink/25',
            )}
          >
            {/* The chosen mark: a slanted yellow marker over the text, with the pink arrow pointing at it. */}
            <span aria-hidden="true" className={cn('absolute -rotate-[1.5deg] bg-riso-yellow opacity-0 mix-blend-multiply group-data-[state=checked]:opacity-100', vertical ? 'inset-y-1 right-2.5 left-1.5' : 'inset-x-1 top-1 bottom-2.5')} />
            <span
              aria-hidden="true"
              className={cn(
                'absolute size-0 opacity-0 group-data-[state=checked]:opacity-100',
                vertical ? 'top-1/2 -left-1 -translate-y-1/2 border-y-[5px] border-l-[7px] border-y-transparent border-l-riso-pink' : '-top-2 left-1/2 -translate-x-1/2 border-x-[5px] border-t-[7px] border-x-transparent border-t-riso-pink',
              )}
            />
            <span className="relative">{mark.text}</span>
            {mark.tick && (
              <span
                aria-hidden="true"
                className={cn('absolute bg-ink/70 group-data-[state=checked]:bg-ink', vertical ? 'top-1/2 right-0 h-0.5 w-2.5 group-data-[state=checked]:w-3.5' : 'bottom-0 left-1/2 h-1.5 w-0.5 group-data-[state=checked]:h-2.5')}
              />
            )}
          </RadioGroupPrimitive.Item>
        ))}
      </RadioGroupPrimitive.Root>
      {unavailable && <p className="px-1 text-[12px] leading-snug text-ink-500">{copy.unavailable(label(value))}</p>}
    </div>
  )
}
