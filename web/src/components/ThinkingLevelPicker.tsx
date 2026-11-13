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

// 单选组里“默认”一段的值（契约里是 null）。
const DEFAULT = '__default__'

// 思考等级：一组分段按钮（2px 藏青描边、12px 圆角，选中的一段黄色），单选组的语义，左右方向键在段之间移动。
// 第一段是“默认”（写明默认是哪一级），其余各段只取所选模型目录里的等级。存着的等级不在目录里时没有哪一段选中，下面写明当前值暂不可选。
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
            // “默认”一段按字的宽度排（它写着默认是哪一级），各级平分剩下的宽度。
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
