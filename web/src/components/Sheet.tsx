import type { ComponentProps, ElementType, Ref } from 'react'
import { cn } from '@/lib/utils'

// 纸张：页面主体放在一张纸上。浅纸底（带主区那层很淡的颗粒）、2px 藏青描边、18px 圆角、6px 孔版蓝实色投影；右侧留 6px 给投影。
// 内容超出时由里面的区域自己滚动，纸张本身裁掉溢出的部分。
export function Sheet({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      data-sheet
      className={cn('riso-grain mr-1.5 flex min-h-0 flex-col overflow-hidden rounded-panel border-2 border-ink bg-paper-raised shadow-print-dialog', className)}
      {...props}
    />
  )
}

// 票根：纸张右侧用 2px 虚线隔出的一条，里面是一把尺子（对话页是轮次，配置页是目录，文件夹与收件箱是几个大数字）。
// 顶部一个小标题。窄屏（左侧栏收进抽屉时）不显示。作为导航区（as="nav"）并用 aria-label 命名时，小标题不再读一遍。
export function Stub({
  as: Tag = 'div',
  label,
  className,
  children,
  ...props
}: { as?: ElementType; label: string; ref?: Ref<HTMLElement> } & Omit<ComponentProps<'div'>, 'ref'>) {
  return (
    <Tag className={cn('flex shrink-0 flex-col gap-2.5 border-l-2 border-dashed border-ink/40 pt-4 pb-3.5 max-nav:hidden', className)} {...props}>
      <div aria-hidden={props['aria-label'] ? true : undefined} className="shrink-0 text-center indent-[0.3em] text-[11px] leading-[1.75] font-black tracking-[0.3em] text-ink-500">
        {label}
      </div>
      {children}
    </Tag>
  )
}

// 尺子右缘的一排刻度（每 23px 一条 2px 的短线），纯装饰；放在定位容器里，占满它的高度。
export function RulerTicks({ className, ...props }: ComponentProps<'div'>) {
  return (
    <div
      aria-hidden="true"
      className={cn('pointer-events-none absolute top-0 right-0 h-full w-3 bg-[repeating-linear-gradient(to_bottom,var(--ink)_0,var(--ink)_2px,transparent_2px,transparent_23px)]', className)}
      {...props}
    />
  )
}

// 尺子上标出当前位置的一笔：略微倾斜的黄色记号笔，左侧一个粉色小箭头。纯装饰；位置与高度由调用方给（style 或类名）。
export function RulerMark({ className, ...props }: ComponentProps<'span'>) {
  return (
    <span aria-hidden="true" className={cn('pointer-events-none absolute right-0 left-0 block', className)} {...props}>
      <span data-ruler-band className="absolute inset-y-0 -right-0.5 left-2 -rotate-[1.5deg] bg-riso-yellow mix-blend-multiply" />
      <span data-ruler-arrow className="absolute top-1/2 left-0.5 size-0 -translate-y-1/2 border-y-[6px] border-l-8 border-y-transparent border-l-riso-pink" />
    </span>
  )
}

// 票根里的一个大数字（文件夹页的文件数、收件箱的未读数……）：上面是 Bricolage 34px 的数，下面是一行小字说明，靠右排。
export function StubFigure({ value, label, tone = 'blue' }: { value: string; label: string; tone?: 'blue' | 'ink' | 'pink' }) {
  return (
    <div className="flex flex-col items-end gap-0.5">
      <span className={cn('font-display-latin text-[34px] leading-none font-extrabold', tone === 'blue' ? 'text-riso-blue' : tone === 'pink' ? 'text-pink-text' : 'text-ink')}>{value}</span>
      <span className="text-[12.5px] font-black text-ink-700">{label}</span>
    </div>
  )
}
