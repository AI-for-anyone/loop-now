import { cn } from '@/lib/utils'

// The small arrow on the right of the dashed-box dropdown (scope selection): decorative, does not block clicks. Size is set in class
// names, not the width attribute (so it is not mistaken for a chart on the page).
export function SelectChevron({ className }: { className?: string }) {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.8} strokeLinecap="round" strokeLinejoin="round" className={cn('pointer-events-none absolute right-1.5 size-3', className)}>
      <path d="m6 9 6 6 6-6" />
    </svg>
  )
}
