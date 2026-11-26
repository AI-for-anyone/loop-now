import { cn } from './utils'

// Index tab: a tab stuck to the top edge of the sheet, bordered only on the top, left and right. The current page's tab is yellow and
// 40px high; the others are beige-gray and 34px high, and a row of tabs is aligned at the bottom.
// Used by both links (page tabs) and buttons (status filters, categories).
// The container for a row of index tabs: sits right on the top edge of the sheet below, aligned with the page header's horizontal
// gutters, bottoms aligned; on narrow screens it can scroll horizontally, with 6px left above for focus rings.
export const indexTabsClass =
  'relative z-[1] flex shrink-0 items-end gap-1.5 pl-6 max-nav:overflow-x-auto max-nav:pt-1.5 max-nav:pl-3.5 max-nav:no-scrollbar'

export const indexTabClass = (current: boolean) =>
  cn(
    'inline-flex shrink-0 items-center gap-2 rounded-t-input border-2 border-b-0 border-ink text-[14px] font-black whitespace-nowrap no-underline outline-none focus-visible:shadow-focus max-sm:px-2.5',
    current ? 'h-10 bg-riso-yellow px-[22px] text-ink' : 'h-[34px] bg-paper-tab px-[18px] text-ink-700 hover:text-ink',
  )
