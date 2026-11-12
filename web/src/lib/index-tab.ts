import { cn } from './utils'

// 索引标签：贴在纸张上沿的分页标签，只有上、左、右三边描边。当前页黄色、高 40px；其余米灰色、高 34px，一排标签底边对齐。
// 链接（页签）与按钮（状态筛选、分类）都用它。
export const indexTabClass = (current: boolean) =>
  cn(
    'inline-flex shrink-0 items-center gap-2 rounded-t-[12px] border-2 border-b-0 border-ink text-[14px] font-black no-underline outline-none focus-visible:shadow-focus',
    current ? 'h-10 bg-riso-yellow px-[22px] text-ink' : 'h-[34px] bg-paper-tab px-[18px] text-ink-700 hover:text-ink',
  )
