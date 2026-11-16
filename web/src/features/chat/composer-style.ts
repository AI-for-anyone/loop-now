// 输入栏（对话页、任务节点）共用的样式与按键说明。
export const mac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)

// 纸张底部输入区里的按键：浅纸底、藏青描边。
export const barKbdClass = 'rounded-stamp border-[1.5px] border-ink bg-paper-raised px-1.5 font-sans text-[11px] leading-[17px] font-black text-ink'
