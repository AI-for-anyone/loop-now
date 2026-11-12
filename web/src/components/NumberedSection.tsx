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

// 编号分区：长页面按 01、02…分区。左侧空心编号，标题是 ZCOOL 30px 加一笔黄色记号笔（下半部黄色条）。
export function NumberedSection({ number, title, className, children, ...props }: { number: number; title: ReactNode } & Omit<ComponentProps<'section'>, 'title'>) {
  const titleId = useId()
  return (
    <section aria-labelledby={titleId} className={cn('flex gap-[18px]', className)} {...props}>
      <HollowNumber className="pt-0.5">{String(number).padStart(2, '0')}</HollowNumber>
      <div className="flex min-w-0 flex-1 flex-col gap-[18px]">
        <h2 id={titleId} className="riso-marker self-start px-1.5 font-display-cn text-[30px] leading-[1.25] font-normal">
          {title}
        </h2>
        {children}
      </div>
    </section>
  )
}
