import { t } from '@/i18n'
import { cn } from '@/lib/utils'
import { useTheme } from '@/theme/store'

// Bold metallic silver ("Loop") + champagne gold ("Now") brand wordmark in the default skin,
// echoing the obsidian/silver/gold desktop app icon; preserves classic Risograph display styling in Plate skin.
export function BrandWordmark({ className }: { className?: string }) {
  const { skin, resolvedTheme } = useTheme()
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

  return (
    <span
      data-brand-wordmark
      className={cn('inline-flex items-baseline gap-[0.26em] whitespace-pre font-sans text-[15px] leading-none font-extrabold tracking-[-0.03em] select-none', className)}
    >
      <span
        className={cn(
          'bg-gradient-to-b bg-clip-text text-transparent',
          dark
            ? 'from-[#FFFFFF] via-[#D4D4D8] to-[#7E8594]'
            : 'from-[#8A94A6] via-[#475569] to-[#0F172A]',
        )}
      >
        {first}
      </span>
      {rest && ' '}
      {rest && (
        <span
          className={cn(
            'bg-gradient-to-b bg-clip-text text-transparent',
            dark
              ? 'from-[#FFF3B0] via-[#F5C542] to-[#C2851A]'
              : 'from-[#F59E0B] via-[#D4820A] to-[#8C4A08]',
          )}
        >
          {rest}
        </span>
      )}
    </span>
  )
}
