import { cn } from '@/lib/utils'

// The spark of writing with AI: a four-pointed star with a small one beside it, on a 24×24 grid, in the text color.
const SPARK = 'M10 3.6c.7 4.4 2.9 6.6 7.3 7.3-4.4.7-6.6 2.9-7.3 7.3-.7-4.4-2.9-6.6-7.3-7.3 4.4-.7 6.6-2.9 7.3-7.3z'
const SMALL = 'M18.6 2.4c.3 1.8 1.2 2.7 3 3-1.8.3-2.7 1.2-3 3-.3-1.8-1.2-2.7-3-3 1.8-.3 2.7-1.2 3-3z'

export function Spark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" className={cn('size-4 shrink-0', className)}>
      <path d={SPARK} fill="currentColor" />
      <path d={SMALL} fill="currentColor" />
    </svg>
  )
}

// "Automatic" among the runtimes' marks (components/runner-picker/RuntimeMark): a single-color ink-frame badge matching the runtime marks.
export function AutomaticMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 32 32" aria-hidden="true" className={cn('size-8 shrink-0 overflow-visible', className)}>
      <rect x="3.8" y="4.8" width="24.4" height="22.4" rx="7" fill="var(--surface-control)" stroke="var(--hairline-strong)" strokeWidth="1.5" />
      <g transform="translate(6.2 6.2) scale(0.82)">
        <path d={SPARK} fill="var(--ink)" />
        <path d={SMALL} fill="var(--ink)" />
      </g>
    </svg>
  )
}
