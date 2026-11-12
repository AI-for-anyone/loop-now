// 小票：一行分成几段，段与段之间一道 1.5px 的虚线，浅蓝底。运行记录的触发条和命令结果的抬头都用它。
export const receiptClass = 'inline-flex max-w-full flex-wrap items-stretch rounded-[6px] bg-tint-blue text-[12px] leading-7 text-ink'

// 第一段之后的各段：左边一道虚线。
export const receiptPartClass = 'border-l-[1.5px] border-dashed border-ink/40 px-[11px] whitespace-nowrap'
