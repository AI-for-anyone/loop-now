// Unified Vercel Geist segmented control classes: recessed track with elevated active pill (zero harsh internal dividers) in mono skin;
// pill with 2px ink borders and yellow active fill in Plate skin.
// Used on ToggleGroup: segmentedClass goes on the ToggleGroup, segmentedItemClass on each ToggleGroupItem.
export const segmentedClass =
  'h-8 flex-nowrap items-center gap-0.5 rounded-[8px] border border-hairline bg-segment-track p-[3px]'

export const segmentedItemClass =
  'h-full min-h-0 rounded-[5px] px-2.5 text-[12.5px] font-medium text-ink-500 transition-[background-color,color,box-shadow] duration-150 hover:text-ink focus-visible:shadow-focus data-[state=on]:bg-segment-active data-[state=on]:font-medium data-[state=on]:text-ink data-[state=on]:shadow-[0_1px_2px_rgb(0_0_0/0.08),0_0_0_1px_var(--hairline)]'
