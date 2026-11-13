import { cn } from './utils'

// 左侧栏的一行：17px 线性图标在前，一般 36px 高，底部的全局入口 34px（compact）。选中项是黄色块，2px 藏青描边加 4px 藏青实色投影
// （与项目卡片一致），右侧给投影留 3px；选中的 Agent 行高 40px、圆角大一档。未选中的也留出透明描边，选中前后文字不移位。
// Agent 行不固定高度（至少 36px，选中时 40px）：名字放不下右边的标记时，标记换到下一行，行随内容长高。
export const navRowClass = (current: boolean, kind: 'nav' | 'agent' | 'compact' = 'nav') =>
  cn(
    'flex min-w-0 items-center border-2 px-2 outline-none focus-visible:shadow-focus',
    kind === 'agent' ? 'gap-2.5' : 'gap-3',
    kind === 'compact' ? 'h-[34px] text-[14px]' : 'text-[14.5px]',
    kind === 'agent' ? (current ? 'min-h-10 rounded-input py-1' : 'min-h-9 rounded-ticket py-1') : kind === 'compact' ? 'rounded-ticket' : 'h-9 rounded-ticket',
    current ? 'mr-[3px] border-ink bg-riso-yellow font-black text-ink shadow-[4px_4px_0_var(--ink)]' : 'border-transparent font-bold text-white hover:bg-white/10',
  )

// 分组标题（AGENT、项目）：黄色的 Bricolage 小字、加宽字距，右侧可以放一个新建按钮。
export const navGroupClass = 'mt-2 flex h-[26px] items-center pr-1 pl-2.5 font-display-latin text-[13px] font-extrabold tracking-[0.1em] text-riso-yellow uppercase'

// 分组标题右侧的“+”：黄底小方块，藏青描边与偏移投影。
export const navAddClass =
  'ml-auto inline-flex size-[26px] items-center justify-center rounded-[8px] border-2 border-ink bg-riso-yellow text-ink shadow-[2px_2px_0_var(--ink)] outline-none focus-visible:shadow-focus disabled:opacity-50'
