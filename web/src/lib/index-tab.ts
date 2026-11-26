import { cn } from './utils'

// Index tabs: hairline bottom-border underline tabs in mono skin; physical yellow/beige folder tabs stuck to the sheet edge in Plate skin.
// Used by both links (page tabs) and buttons (status filters, categories).
export const indexTabsClass =
  'relative z-[1] -mb-px flex shrink-0 items-end gap-5 border-b border-hairline max-nav:overflow-x-auto max-nav:pt-1.5 max-nav:no-scrollbar [[data-skin=plate]_&]:mb-0 [[data-skin=plate]_&]:gap-1.5 [[data-skin=plate]_&]:border-b-0 [[data-skin=plate]_&]:pl-[26px] [[data-skin=plate]_&]:max-nav:pl-3'

export const indexTabClass = (current: boolean) =>
  cn(
    'inline-flex shrink-0 items-center gap-2 -mb-px border-b-2 pt-2 pb-2.5 text-[13.5px] whitespace-nowrap no-underline outline-none transition-colors focus-visible:shadow-focus [[data-skin=plate]_&]:mb-0 [[data-skin=plate]_&]:rounded-t-input [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-b-0 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:py-0 [[data-skin=plate]_&]:text-[14px] [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:max-sm:px-2.5',
    current
      ? 'border-ink font-semibold text-ink [[data-skin=plate]_&]:h-10 [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:px-[22px] [[data-skin=plate]_&]:text-ink'
      : 'border-transparent font-medium text-ink-500 hover:text-ink [[data-skin=plate]_&]:h-[34px] [[data-skin=plate]_&]:bg-paper-tab [[data-skin=plate]_&]:px-[18px] [[data-skin=plate]_&]:text-ink-700 [[data-skin=plate]_&]:hover:text-ink',
  )
