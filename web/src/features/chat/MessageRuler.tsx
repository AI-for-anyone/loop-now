import { PrecisionRuler, type PrecisionRulerItem } from '@/components/PrecisionRuler'
import { t } from '@/i18n'

export interface RulerTurn {
  key: string
  label: string
}

interface MessageRulerProps {
  turns: RulerTurn[]
  visible: ReadonlySet<string>
  onJump: (key: string) => void
  // Jump to the true bottom of the conversation and re-enable auto-follow.
  onJumpBottom?: () => void
  // The turn number of the first tick. null when unknown (earlier history isn't fully loaded and no count of the preceding turns was
  // given): only ticks are drawn, without numbers.
  firstNumber: number | null
  // The total turn count at the bottom; not shown when unknown.
  total: number | null
  className?: string
}

// Floating vernier tick rail for conversation turns, built on the global PrecisionRuler.
// Hidden automatically when fewer than 2 turns exist and steals zero horizontal layout width.
export function MessageRuler({ turns, visible, onJump, onJumpBottom, firstNumber, total, className }: MessageRulerProps) {
  const items: PrecisionRulerItem[] = turns.map((turn, index) => {
    const number = firstNumber === null ? null : firstNumber + index
    return {
      id: turn.key,
      label: turn.label,
      badge: number !== null ? `#${number}` : undefined,
      major: number !== null && number % 5 === 0,
    }
  })

  return (
    <PrecisionRuler
      items={items}
      activeIds={visible}
      onSelect={onJump}
      onJumpBottom={onJumpBottom}
      jumpBottomLabel={t.chat.ruler.latest}
      total={total}
      totalLabel={total !== null ? t.chat.ruler.total(total) : undefined}
      label={t.chat.ruler.label}
      placement="floating"
      minItems={2}
      className={className}
    />
  )
}
