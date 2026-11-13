import { cn } from '@/lib/utils'

// 虚线框下拉（范围选择）右边的小箭头：装饰，不挡点击。尺寸写在类名里，不用 width 属性（不被当成页面上的图表）。
export function SelectChevron({ className }: { className?: string }) {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2.8} strokeLinecap="round" strokeLinejoin="round" className={cn('pointer-events-none absolute right-1.5 size-3', className)}>
      <path d="m6 9 6 6 6-6" />
    </svg>
  )
}
