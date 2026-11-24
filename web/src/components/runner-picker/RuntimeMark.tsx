import type { ReactNode } from 'react'
import type { AgentRuntime, RuntimeDefinition } from '@/lib/runtime'
import { cn } from '@/lib/utils'

// A runtime's mark, drawn in the console's riso print language rather than taken from a product's logo: two misregistered inks and a
// paper-colored glyph. The shape says what kind of runtime it is (a window for a CLI installed on this computer, a pressed disc for one
// that ships with the platform), the glyph which one; a runtime without a glyph of its own gets its initial.
const glyphs: Partial<Record<AgentRuntime, ReactNode>> = {
  // A terminal prompt: the CLI runs in a terminal on this computer.
  'claude-code': (
    <g fill="none" stroke="var(--paper-raised)" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round">
      <path d="M9.5 14.5l4.2 3.6-4.2 3.6" />
      <path d="M16.4 22.4h6.4" />
    </g>
  ),
  // π, drawn as strokes: a bar with two legs, the right one kicking out.
  pi: (
    <g fill="none" stroke="var(--ink)" strokeWidth="2.6" strokeLinecap="round" strokeLinejoin="round">
      <path d="M9.6 12.2c1.3-.9 2.6-1.2 4.2-1.2h8.8" />
      <path d="M13.6 11.4c-.2 4-.9 7.4-2.4 10.4" />
      <path d="M19.4 11.4c-.4 3.4-.3 6.3.4 8.2.5 1.3 1.6 1.9 2.8 1.3" />
    </g>
  ),
}

export function RuntimeMark({ runtime, className }: { runtime: RuntimeDefinition; className?: string }) {
  const glyph = glyphs[runtime.id] ?? (
    <text x="16" y="21.6" textAnchor="middle" fontSize="13" fontWeight="800" fontFamily="var(--font-display-latin)" fill={runtime.capabilities.bundled ? 'var(--ink)' : 'var(--paper-raised)'}>
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
          <circle cx="9.8" cy="7.1" r="1.1" fill="var(--paper-raised)" opacity=".7" />
        </>
      )}
      {glyph}
    </svg>
  )
}
