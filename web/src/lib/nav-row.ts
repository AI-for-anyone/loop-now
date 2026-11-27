import { cn } from './utils'

// A sidebar row: single-line across every nav, compact and Agent entry so long names truncate cleanly across dragged sidebar widths.
// Unselected rows keep a transparent border of matching width so text does not shift when selection changes.
export const navRowClass = (current: boolean, kind: 'nav' | 'agent' | 'compact' = 'nav') =>
  cn(
    'flex min-w-0 items-center border border-transparent px-2 transition-colors outline-none focus-visible:shadow-focus',
    kind === 'agent' ? 'gap-2' : 'gap-2.5',
    kind === 'compact'
      ? 'h-[30px] rounded-ticket text-[13px]'
      : kind === 'agent'
        ? current
          ? 'h-9 rounded-input text-[13px]'
          : 'h-8 rounded-ticket text-[13px]'
        : 'h-8 rounded-ticket text-[13px]',
    current
      ? 'bg-sidebar-active-bg font-semibold text-sidebar-active-fg shadow-surface'
      : 'font-medium text-sidebar-muted hover:bg-sidebar-hover hover:text-sidebar-fg',
  )

// Group headings (Agents, Projects): uppercase semantic label; a create button can sit on the right.
export const navGroupClass =
  'mt-2 flex h-[26px] min-w-0 items-center gap-2 pr-1 pl-2.5 font-display-latin text-[11.5px] font-semibold tracking-[0.08em] text-sidebar-group uppercase'

// The "+" on the right of a group heading: borderless ghost icon button matching sidebar controls.
export const navAddClass =
  'ml-auto inline-flex size-6 shrink-0 cursor-pointer items-center justify-center rounded-ticket border-0 bg-transparent text-sidebar-muted transition-colors outline-none hover:bg-sidebar-hover hover:text-sidebar-fg focus-visible:shadow-focus disabled:pointer-events-none disabled:opacity-40'
