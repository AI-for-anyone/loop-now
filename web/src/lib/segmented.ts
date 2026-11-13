// 分段按钮（改版）：2px 藏青描边的胶囊，里面几段用 2px 竖线隔开，选中的一段是黄色（选中态一律黄色）。
// 用在 ToggleGroup 上：segmentedClass 给 ToggleGroup，segmentedItemClass 给每个 ToggleGroupItem。
export const segmentedClass = 'h-8 flex-nowrap gap-0 overflow-hidden rounded-full border-2 border-ink bg-paper-raised p-0'

export const segmentedItemClass =
  'h-full min-h-0 rounded-none px-3.5 text-[12.5px] text-ink-700 not-first:border-l-2 not-first:border-ink focus-visible:shadow-[inset_0_0_0_3px_var(--riso-blue)] data-[state=on]:bg-riso-yellow data-[state=on]:text-ink data-[state=on]:shadow-none data-[state=on]:focus-visible:shadow-[inset_0_0_0_3px_var(--riso-blue)]'
