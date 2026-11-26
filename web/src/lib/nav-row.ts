import { cn } from './utils'

// A sidebar row: a 17px line icon first, normally 36px high, 34px for the global entries at the bottom (compact). The selected item is a
// yellow block with a 2px navy border plus a 4px navy solid offset shadow
// (matching the project cards), with 3px left on the right for the shadow; a selected Agent row is 40px high with a one-step larger corner
// radius. Unselected rows keep a transparent border too, so the text does not shift when the selection changes.
// Agent rows have no fixed height (at least 36px, 40px when selected): when the markers on the right do not fit next to the name, they
// wrap to the next line and the row grows with its content.
export const navRowClass = (current: boolean, kind: 'nav' | 'agent' | 'compact' = 'nav') =>
  cn(
    'flex min-w-0 items-center border-2 px-2 outline-none focus-visible:shadow-focus',
    kind === 'agent' ? 'gap-2.5' : 'gap-3',
    kind === 'compact' ? 'h-[34px] text-[14px]' : 'text-[14.5px]',
    kind === 'agent' ? (current ? 'min-h-10 rounded-input py-1' : 'min-h-9 rounded-ticket py-1') : kind === 'compact' ? 'rounded-ticket' : 'h-9 rounded-ticket',
    current ? 'mr-[3px] border-ink bg-riso-yellow font-black text-ink shadow-[4px_4px_0_var(--ink)]' : 'border-transparent font-bold text-white hover:bg-white/10',
  )

// Group headings (AGENT, “项目” (Projects)): small yellow Bricolage text with wide letter spacing; a create button can sit on the right.
export const navGroupClass = 'mt-2 flex h-[26px] min-w-0 items-center gap-2 pr-1 pl-2.5 font-display-latin text-[13px] font-extrabold tracking-[0.1em] text-riso-yellow uppercase'

// The "+" on the right of a group heading: a small yellow square with a navy border and an offset shadow.
export const navAddClass =
  'ml-auto inline-flex size-[26px] shrink-0 items-center justify-center rounded-[8px] border-2 border-ink bg-riso-yellow text-ink shadow-[2px_2px_0_var(--ink)] outline-none focus-visible:shadow-focus disabled:opacity-50'
