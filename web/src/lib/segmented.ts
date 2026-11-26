// Segmented buttons: flat 1px hairline control in mono skin; pill with 2px ink borders and yellow active fill in Plate skin.
// Used on ToggleGroup: segmentedClass goes on the ToggleGroup, segmentedItemClass on each ToggleGroupItem.
export const segmentedClass =
  'h-8 flex-nowrap gap-0 overflow-hidden rounded-ticket border border-hairline-strong bg-surface-control p-0 [[data-skin=plate]_&]:rounded-full [[data-skin=plate]_&]:border-2 [[data-skin=plate]_&]:border-ink [[data-skin=plate]_&]:bg-paper-raised'

export const segmentedItemClass =
  'h-full min-h-0 rounded-none px-3.5 text-[12.5px] font-medium text-ink-500 not-first:border-l not-first:border-hairline hover:bg-surface-hover hover:text-ink focus-visible:shadow-focus data-[state=on]:bg-surface-active data-[state=on]:font-semibold data-[state=on]:text-ink data-[state=on]:shadow-none [[data-skin=plate]_&]:font-black [[data-skin=plate]_&]:text-ink-700 [[data-skin=plate]_&]:not-first:border-l-2 [[data-skin=plate]_&]:not-first:border-ink [[data-skin=plate]_&]:hover:bg-transparent [[data-skin=plate]_&]:focus-visible:shadow-[inset_0_0_0_3px_var(--riso-blue)] [[data-skin=plate]_&]:data-[state=on]:bg-riso-yellow [[data-skin=plate]_&]:data-[state=on]:text-ink [[data-skin=plate]_&]:data-[state=on]:focus-visible:shadow-[inset_0_0_0_3px_var(--riso-blue)]'
