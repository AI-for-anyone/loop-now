import { cn } from './utils'

// Index tabs: flush 40px hairline sub-navigation bar shared by links (page tabs) and buttons (status filters, categories).
export const indexTabsClass =
  'relative z-[1] flex h-10 shrink-0 items-stretch gap-5 overflow-x-auto border-b border-hairline bg-surface-canvas px-4 no-scrollbar sm:px-6'

export const indexTabClass = (current: boolean) =>
  cn(
    'inline-flex shrink-0 items-center gap-1.5 -mb-px border-b-2 text-[14px] whitespace-nowrap no-underline outline-none transition-colors focus-visible:shadow-focus',
    current
      ? 'border-ink font-semibold text-ink'
      : 'border-transparent font-medium text-ink-500 hover:text-ink',
  )
