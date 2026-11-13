import { useId, type ComponentProps, type ReactNode } from 'react'
import { cn } from '@/lib/utils'

// 空心编号：Bricolage 800、孔版蓝描边、不填色。分区的 01、02 与对话里每一轮的编号都用它。纯装饰，窄屏不显示。
export function HollowNumber({ className, ...props }: ComponentProps<'span'>) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        'block w-[46px] shrink-0 font-display-latin text-[34px] leading-none font-extrabold tracking-[-0.02em] text-transparent [-webkit-text-stroke:1.6px_var(--riso-blue)] max-nav:hidden',
        className,
      )}
      {...props}
    />
  )
}

// 编号分区：长页面按 01、02…分区（改版通用规则第 4 条）。标题一行：左侧空心编号，标题是 ZCOOL 30px 加一笔黄色记号笔
// （下半部黄色条），后面可以跟一段说明（meta）和靠右的操作（actions）；内容排在下面，占满宽度。
// 从第二个分区起，上方一道 2px 虚线。
export function NumberedSection({
  number,
  title,
  meta,
  actions,
  className,
  children,
  ...props
}: { number: number; title: ReactNode; meta?: ReactNode; actions?: ReactNode } & Omit<ComponentProps<'section'>, 'title'>) {
  const titleId = useId()
  return (
    <section aria-labelledby={titleId} className={cn('flex min-w-0 flex-col gap-[18px]', number > 1 && 'border-t-2 border-dashed border-ink/[0.22] pt-6', className)} {...props}>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2.5">
        <HollowNumber data-section-number>{String(number).padStart(2, '0')}</HollowNumber>
        <h2 id={titleId} className="riso-marker px-1.5 font-display-cn text-[30px] leading-[1.25] font-normal">
          {title}
        </h2>
        {meta}
        {actions && <div className="flex flex-wrap items-center gap-2 sm:ml-auto">{actions}</div>}
      </div>
      {children}
    </section>
  )
}
