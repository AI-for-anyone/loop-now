import { runtimeGlyph, type RuntimeDefinition } from '@/lib/runtime'
import { cn } from '@/lib/utils'
import { useTheme } from '@/theme/store'

// A runtime's mark: crisp monochrome geometric mark in mono skin (inverted terminal window for local CLIs, outlined disc for the bundled
// runtime); two misregistered riso inks in Plate skin. The glyph comes from lib/runtime.ts, or falls back to the runtime's initial.
export function RuntimeMark({ runtime, className }: { runtime: RuntimeDefinition; className?: string }) {
  const { skin } = useTheme()
  const isPlate = skin === 'plate'
  const color = runtime.capabilities.bundled ? 'var(--ink)' : isPlate ? 'var(--paper-raised)' : 'var(--cta-fg)'
  const strokes = runtimeGlyph(runtime.id)
  const glyph = strokes ? (
    <g fill="none" stroke={color} strokeWidth={isPlate ? '2.6' : '2.4'} strokeLinecap="round" strokeLinejoin="round">
      {strokes.map((d) => (
        <path key={d} d={d} />
      ))}
    </g>
  ) : (
    <text x="16" y="21.6" textAnchor="middle" fontSize="13" fontWeight={isPlate ? '800' : '700'} fontFamily="var(--font-display-latin)" fill={color}>
      {runtime.name.slice(0, 1).toUpperCase()}
    </text>
  )

  if (!isPlate) {
    return (
      <svg viewBox="0 0 32 32" aria-hidden="true" className={cn('size-8 shrink-0', className)}>
        {runtime.capabilities.bundled ? (
          <circle cx="16" cy="16" r="13" fill="var(--surface-control)" stroke="var(--hairline-strong)" strokeWidth="1.5" />
        ) : (
          <>
            <rect x="2.5" y="4" width="27" height="24" rx="5.5" fill="var(--cta-bg)" />
            <path d="M2.5 10h27" stroke="var(--cta-fg)" strokeWidth="1.2" opacity=".32" />
            <circle cx="6.5" cy="7.1" r="1.1" fill="var(--cta-fg)" opacity=".75" />
            <circle cx="10" cy="7.1" r="1.1" fill="var(--cta-fg)" opacity=".45" />
          </>
        )}
        {glyph}
      </svg>
    )
  }

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
          <circle cx="9.8" cy="7.1" r="1.1" fill="var(--paper-raised)" opacity=".7" />
        </>
      )}
      {glyph}
    </svg>
  )
}

// The glyph alone, in the text color, for small places (the Agent header's runtime badge, a sidebar row): a runtime's own strokes, or its
// initial.
export function RuntimeGlyph({ runtime, className }: { runtime: RuntimeDefinition; className?: string }) {
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
