import { BrandIcon } from '@/components/BrandIcon'
import { runtimeBrandIcon, runtimeGlyph, type RuntimeDefinition } from '@/lib/runtime'
import { cn } from '@/lib/utils'
import { useTheme } from '@/theme/store'

// A runtime's mark: official @lobehub/icons brand logo in mono skin; two misregistered riso inks in Plate skin.
// The logo mapping lives exclusively in lib/runtime.ts (checked by check-runtime-ids.mjs).
export function RuntimeMark({ runtime, className }: { runtime: RuntimeDefinition; className?: string }) {
  const { skin } = useTheme()
  if (skin !== 'plate') {
    return <BrandIcon name={runtimeBrandIcon(runtime)} className={cn('size-4 shrink-0', className)} />
  }

  const color = runtime.capabilities.bundled ? 'var(--ink)' : 'var(--paper-raised)'
  const strokes = runtimeGlyph(runtime.id)
  const glyph = strokes ? (
    <g fill="none" stroke={color} strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round">
      {strokes.map((d) => (
        <path key={d} d={d} />
      ))}
    </g>
  ) : (
    <text x="16" y="21.6" textAnchor="middle" fontSize="13" fontWeight="800" fontFamily="var(--font-display-latin)" fill={color}>
      {runtime.name.slice(0, 1).toUpperCase()}
    </text>
  )

  return (
    <svg viewBox="0 0 32 32" aria-hidden="true" className={cn('size-8 shrink-0 overflow-visible', className)}>
      {runtime.capabilities.bundled ? (
        <>
          {/* The pink plate printed a little off register, under a yellow disc with an ink rim. */}
          <circle cx="17.6" cy="17.6" r="12" fill="var(--riso-pink)" />
          <circle cx="16" cy="16" r="12" fill="var(--riso-yellow)" stroke="var(--ink)" strokeWidth="2" style={{ mixBlendMode: 'multiply' }} />
        </>
      ) : (
        <>
          <rect x="4.2" y="5.6" width="25" height="22" rx="5" fill="var(--riso-pink)" />
          <rect x="2.6" y="4" width="25" height="22" rx="5" fill="var(--riso-blue)" style={{ mixBlendMode: 'multiply' }} />
          {/* The window's title bar: a rule and two dots in paper color. */}
          <path d="M2.6 10h25" stroke="var(--paper-raised)" strokeWidth="1.4" opacity=".55" />
          <circle cx="6.4" cy="7.1" r="1.1" fill="var(--paper-raised)" />
          <circle cx="9.8" cy="7.1" r="1.1" fill="var(--paper-raised)" />
        </>
      )}
      {glyph}
    </svg>
  )
}

// The glyph alone, for small places (the Agent header's runtime badge, a sidebar row): official brand logo in mono skin, strokes in Plate.
export function RuntimeGlyph({ runtime, className }: { runtime: RuntimeDefinition; className?: string }) {
  const { skin } = useTheme()
  if (skin !== 'plate') {
    return <BrandIcon name={runtimeBrandIcon(runtime)} className={cn('size-3.5 shrink-0', className)} />
  }
  const strokes = runtimeGlyph(runtime.id)
  return (
    <svg viewBox="7 9.5 18 15" aria-hidden="true" className={cn('size-3.5 shrink-0', className)}>
      {strokes ? (
        <g fill="none" stroke="currentColor" strokeWidth="2.8" strokeLinecap="round" strokeLinejoin="round">
          {strokes.map((d) => (
            <path key={d} d={d} />
          ))}
        </g>
      ) : (
        <text x="16" y="22" textAnchor="middle" fontSize="14" fontWeight="800" fontFamily="var(--font-display-latin)" fill="currentColor">
          {runtime.name.slice(0, 1).toUpperCase()}
        </text>
      )}
    </svg>
  )
}
