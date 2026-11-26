import { cn } from './utils'

// A sidebar row: a 17px line icon first, compact 32px height in mono skin (36px in Plate), 30px for compact entries (34px in Plate).
// In the default mono skin, rows use subtle monochrome surfaces and borders. In the Plate skin, the selected item
// becomes a yellow block with a 2px ink border and 4px solid offset shadow, with 3px left on the right for the shadow.
// Unselected rows keep a transparent border of matching width so text does not shift when selection changes.
// Agent rows have no fixed height (at least 32px, 36px when selected in mono; 36px / 40px in Plate): when the markers
// on the right do not fit next to the name, they wrap to the next line and the row grows with its content.
export const navRowClass = (current: boolean, kind: 'nav' | 'agent' | 'compact' = 'nav') =>
  cn(
    'flex min-w-0 items-center border border-transparent px-2 transition-colors outline-none focus-visible:shadow-focus [[data-skin=plate]_&]:border-2',
    kind === 'agent' ? 'gap-2.5' : 'gap-2.5 [[data-skin=plate]_&]:gap-3',
    kind === 'compact'
      ? 'h-[30px] rounded-ticket text-[13px] [[data-skin=plate]_&]:h-[34px] [[data-skin=plate]_&]:text-[14px]'
      : kind === 'agent'
        ? current
          ? 'min-h-9 rounded-input py-1 text-[13px] [[data-skin=plate]_&]:min-h-10 [[data-skin=plate]_&]:text-[14.5px]'
          : 'min-h-8 rounded-ticket py-1 text-[13px] [[data-skin=plate]_&]:min-h-9 [[data-skin=plate]_&]:text-[14.5px]'
        : 'h-8 rounded-ticket text-[13px] [[data-skin=plate]_&]:h-9 [[data-skin=plate]_&]:text-[14.5px]',
    current
      ? 'bg-sidebar-active-bg font-semibold text-sidebar-active-fg [[data-skin=plate]_&]:mr-[3px] [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:shadow-[4px_4px_0_var(--ink)]'
      : 'font-medium text-sidebar-muted hover:bg-sidebar-hover hover:text-sidebar-fg [[data-skin=plate]_&]:font-bold [[data-skin=plate]_&]:text-white',
  )

// Group headings (AGENT, Projects): uppercase semantic label; a create button can sit on the right.
export const navGroupClass =
  'mt-2 flex h-[26px] items-center pr-1 pl-2.5 font-display-latin text-[11.5px] font-semibold tracking-[0.08em] text-sidebar-group uppercase [[data-skin=plate]_&]:text-[13px] [[data-skin=plate]_&]:font-extrabold [[data-skin=plate]_&]:tracking-[0.1em]'

// The "+" on the right of a group heading: clean subtle icon button in mono; yellow ink-bordered square in Plate.
export const navAddClass =
  'ml-auto inline-flex size-[24px] items-center justify-center rounded-stamp border border-sidebar-border bg-transparent text-sidebar-muted transition-colors outline-none hover:bg-sidebar-hover hover:text-sidebar-fg focus-visible:shadow-focus disabled:opacity-50 [[data-skin=plate]_&]:size-[26px] [[data-skin=plate]_&]:rounded-[8px] [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-riso-yellow [[data-skin=plate]_&]:text-ink [[data-skin=plate]_&]:shadow-[2px_2px_0_var(--ink)]'
