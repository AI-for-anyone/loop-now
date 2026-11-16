// Segmented buttons (redesign): a pill with a 2px navy border, its segments separated by 2px vertical lines, the selected segment yellow
// (selected states are always yellow).
// Used on ToggleGroup: segmentedClass goes on the ToggleGroup, segmentedItemClass on each ToggleGroupItem.
export const segmentedClass = 'h-8 flex-nowrap gap-0 overflow-hidden rounded-full border-2 border-ink bg-paper-raised p-0'

export const segmentedItemClass =
  'h-full min-h-0 rounded-none px-3.5 text-[12.5px] text-ink-700 not-first:border-l-2 not-first:border-ink focus-visible:shadow-[inset_0_0_0_3px_var(--riso-blue)] data-[state=on]:bg-riso-yellow data-[state=on]:text-ink data-[state=on]:shadow-none data-[state=on]:focus-visible:shadow-[inset_0_0_0_3px_var(--riso-blue)]'
