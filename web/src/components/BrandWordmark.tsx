import { useRef, useState, type CSSProperties } from 'react'
import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useTheme } from '@/theme/store'

type SweepPhase = 'intro' | 'a' | 'b'

// Minimum interval between hover re-triggers so rapid pointer jitter does not restart the beam mid-flight.
const HOVER_RETRIGGER_MS = 450

// Understated platinum-ink ("Loop") + pale satin-champagne ("Now") brand wordmark in the default skin,
// with a diagonal specular light sweep on initial window open and on hover; preserves classic Risograph styling in Plate skin.
export function BrandWordmark({ className }: { className?: string }) {
  const { skin, resolvedTheme } = useTheme()
  const [sweep, setSweep] = useState<SweepPhase>('intro')
  const lastTriggerRef = useRef(0)
  const brand = t.app.brand

  if (skin === 'plate') {
    return (
      <span
        className={cn(
          'font-display-latin text-[16px] leading-none font-extrabold tracking-[-0.3px] [text-shadow:1.5px_1.5px_0_var(--riso-pink)]',
          className,
        )}
      >
        {brand}
      </span>
    )
  }

  const dark = resolvedTheme === 'dark'
  const space = brand.indexOf(' ')
  const first = space > 0 ? brand.slice(0, space) : brand
  const rest = space > 0 ? brand.slice(space + 1) : ''

  const triggerSweep = () => {
    const now = Date.now()
    if (now - lastTriggerRef.current < HOVER_RETRIGGER_MS) return
    lastTriggerRef.current = now
    setSweep((prev) => (prev === 'a' ? 'b' : 'a'))
  }

  const sheenVars = (
    dark
      ? {
          '--brand-sheen-core': 'rgba(255, 253, 246, 0.96)',
          '--brand-sheen-edge': 'rgba(255, 255, 255, 0.24)',
        }
      : {
          '--brand-sheen-core': 'rgba(255, 255, 255, 0.98)',
          '--brand-sheen-edge': 'rgba(226, 206, 168, 0.42)',
        }
  ) as CSSProperties

  return (
    <span
      data-brand-wordmark
      data-sheen={brand}
      data-sweep={sweep}
      style={sheenVars}
      onMouseEnter={triggerSweep}
      className={cn(
        'brand-wordmark whitespace-pre font-sans text-[15px] leading-none font-extrabold tracking-[-0.03em] select-none',
        className,
      )}
    >
      <span
        className={cn(
          'bg-gradient-to-br bg-clip-text text-transparent',
          dark
            ? 'from-[#F8FAFC] via-[#E5E7EB] to-[#9CA3AF]'
            : 'from-[#111318] via-[#374151] to-[#7C8494]',
        )}
      >
        {first}
      </span>
      {rest && ' '}
      {rest && (
        <span
          className={cn(
            'bg-gradient-to-br bg-clip-text text-transparent',
            dark
              ? 'from-[#F8F5EE] via-[#E6D7B9] to-[#BCA474]'
              : 'from-[#3D3426] via-[#7D6843] to-[#B59B6B]',
          )}
        >
          {rest}
        </span>
      )}
    </span>
  )
}
