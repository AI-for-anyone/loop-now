import { cn } from '@/lib/utils'

// 弹窗与抽屉共用的样子（改版第 5 步，全站统一）。

// 遮罩：藏青 22% 加网点。不做进出场动画。
export const overlayClass =
  'fixed inset-0 isolate z-50 bg-ink/[0.22] [background-image:radial-gradient(rgb(255_255_255/.08)_1px,transparent_1.3px)] [background-size:6px_6px]'

// 弹窗的类别，写在顶边的索引标签上：新建是黄色；危险操作浅红底红字（纸的投影也换成红色）；其余浅蓝底。
export type DialogKind = 'create' | 'danger' | 'confirm' | 'edit' | 'view' | 'token' | 'choose' | 'install' | 'update' | 'action' | 'pull'

// 外层只管位置与宽度（调用方的 className 加在这里，例如 sm:max-w-[560px]）；里面是标签和纸。
export const dialogContentClass =
  'fixed top-1/2 left-1/2 z-50 flex max-h-[calc(100dvh-2rem)] w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 flex-col text-[14.5px] text-ink outline-none'

export const dialogTabClass = (kind: DialogKind) =>
  cn(
    'inline-flex h-9 items-center gap-2 rounded-t-input border-2 border-b-0 border-ink px-[18px] text-[13px] font-black',
    kind === 'create' ? 'bg-riso-yellow text-ink' : kind === 'danger' ? 'bg-tint-red text-red-text' : 'bg-tint-blue text-ink',
  )

// 纸：2px 藏青描边、浅纸底、18 圆角、8px 实色投影（孔版蓝；危险操作红色）。内容超出时纸自己滚动。
export const dialogPaperClass = (kind: DialogKind) =>
  cn(
    'riso-scrollbar relative -mt-0.5 mr-2 mb-2 grid min-h-0 content-start gap-5 overflow-y-auto overscroll-contain rounded-panel border-2 border-ink bg-paper-raised px-[26px] pt-6 pb-[22px] max-sm:px-[18px]',
    kind === 'danger' ? 'shadow-[8px_8px_0_var(--red)]' : 'shadow-[8px_8px_0_var(--riso-blue)]',
  )

// 右上角的关闭：黄色圆章（藏青描边、小投影）。弹窗里位置相对外层（在标签下方 18px），纸滚动时不动。
export const closeStampClass =
  'inline-flex size-9 shrink-0 cursor-pointer items-center justify-center rounded-full border-2 border-ink bg-riso-yellow text-ink shadow-[2px_2px_0_var(--ink)] outline-none hover:bg-[#ffd92e] focus-visible:shadow-focus'

// 按钮区：上方一条虚线。里面的主按钮是荧光粉（每个弹窗最多一个）、次要按钮是浅纸底，都是藏青描边加投影；危险操作的确认是红色。
const footerButtons =
  '[&_[data-variant=default]]:border-2 [&_[data-variant=default]]:border-ink [&_[data-variant=default]]:bg-riso-pink [&_[data-variant=default]]:text-ink [&_[data-variant=default]]:shadow-[3px_3px_0_var(--ink)] ' +
  '[&_[data-variant=secondary]]:border-2 [&_[data-variant=secondary]]:border-ink [&_[data-variant=secondary]]:bg-paper-raised [&_[data-variant=secondary]]:shadow-[3px_3px_0_var(--ink)] [&_[data-variant=secondary]]:hover:bg-white ' +
  '[&_[data-variant=destructive]]:border-2 [&_[data-variant=destructive]]:border-ink [&_[data-slot=button]]:mr-[3px]'

export const dialogFooterClass = cn('flex flex-wrap justify-end gap-3 border-t-2 border-dashed border-ink/30 pt-4', footerButtons)

// 抽屉底部的按钮栏：浅黄色（同对话页输入区），上缘 2px 藏青线；危险动作在左、主按钮在右（调用方给主按钮 ml-auto）。
export const sheetFooterClass = cn('flex shrink-0 flex-wrap items-center gap-x-3 gap-y-2.5 border-t-2 border-ink bg-tint-yellow px-[26px] pt-3.5 pb-4', footerButtons)
