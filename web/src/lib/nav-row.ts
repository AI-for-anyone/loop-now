import { cn } from './utils'

// 左侧栏的一行：统一 34px 高（Agent 行 36px），17px 线性图标在前。选中项是黄色块，藏青描边加 3px 偏移投影；
// 未选中的也留出透明描边，选中前后文字不移位。
export const navRowClass = (current: boolean, tall = false) =>
  cn(
    'flex min-w-0 items-center gap-2.5 rounded-ticket border-2 text-[13px] outline-none focus-visible:shadow-focus',
    tall ? 'h-9 px-2' : 'h-[34px] px-2.5',
    current ? 'border-ink bg-riso-yellow font-black text-ink shadow-print-ink' : 'border-transparent font-bold text-white hover:bg-white/10',
  )

// 分组标题（AGENT、项目）：黄色展示字，右侧可以放一个新建按钮。
export const navGroupClass = 'mt-2.5 flex h-7 items-center px-2.5 font-display-cn text-[15px] font-normal text-riso-yellow uppercase'

// 分组标题右侧的“+”：黄底小方块，藏青描边与偏移投影。
export const navAddClass =
  'ml-auto inline-flex size-6 items-center justify-center rounded-[8px] border-[1.5px] border-ink bg-riso-yellow text-ink shadow-[2px_2px_0_var(--ink)] outline-none focus-visible:shadow-focus disabled:opacity-50'
