// 输入框的外观：白底、12 圆角、内描边；聚焦为藏青内描边加黄色外圈；出错（aria-invalid）为浅红底加朱红内描边。
export const fieldClass =
  'w-full min-w-0 rounded-input border-0 bg-white px-3.5 text-[14.5px] text-ink shadow-[inset_0_0_0_2px_rgb(24_38_94/.18)] outline-none placeholder:text-ink-500 focus-visible:shadow-[inset_0_0_0_2px_var(--ink),0_0_0_4px_var(--riso-yellow)] disabled:cursor-not-allowed disabled:opacity-40 aria-invalid:bg-error-field aria-invalid:shadow-[inset_0_0_0_2px_var(--red)]'

// 原生 select 的外观同输入框，右侧画一个下拉箭头。
// 箭头图标定义在 index.css 的 select-chevron 中（data URI 里有空格，不能写进类名）。
export const selectClass = `${fieldClass} select-chevron h-11 appearance-none pr-10`
