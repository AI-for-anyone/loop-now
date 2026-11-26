// Receipt: one line split into hairline segments on a subtle surface in mono skin; light blue with 1.5px dashed dividers in Plate skin.
// Used by the toggle strip of the run log and the header of command results.
export const receiptClass =
  'inline-flex max-w-full flex-wrap items-stretch rounded-ticket border border-hairline bg-surface-subtle text-[12px] leading-7 text-ink [[data-skin=plate]_&]:rounded-[6px] [[data-skin=plate]_&]:border-0 [[data-skin=plate]_&]:bg-tint-blue'

// Every segment after the first: a hairline divider on the left (dashed in Plate skin).
export const receiptPartClass =
  'border-l border-hairline px-[11px] whitespace-nowrap [[data-skin=plate]_&]:border-l-[1.5px] [[data-skin=plate]_&]:border-dashed [[data-skin=plate]_&]:border-ink/40'
