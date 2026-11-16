import { cn } from './utils'

// 索引标签：贴在纸张上沿的分页标签，只有上、左、右三边描边。当前页黄色、高 40px；其余米灰色、高 34px，一排标签底边对齐。
// 链接（页签）与按钮（状态筛选、分类）都用它。
// 一排索引标签的容器：紧贴下面那张纸的上沿，左侧缩进 26px，底边对齐；窄屏时可以横向滚动，上方留 6px 给焦点环。
export const indexTabsClass =
  'relative z-[1] flex shrink-0 items-end gap-1.5 pl-[26px] max-nav:overflow-x-auto max-nav:pt-1.5 max-nav:pl-3 max-nav:no-scrollbar'

export const indexTabClass = (current: boolean) =>
  cn(
    'inline-flex shrink-0 items-center gap-2 rounded-t-input border-2 border-b-0 border-ink text-[14px] font-black whitespace-nowrap no-underline outline-none focus-visible:shadow-focus max-sm:px-2.5',
    current ? 'h-10 bg-riso-yellow px-[22px] text-ink' : 'h-[34px] bg-paper-tab px-[18px] text-ink-700 hover:text-ink',
  )
