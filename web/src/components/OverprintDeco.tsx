import { cn } from '@/lib/utils'

// 页头右上角的装饰：一片网点，蓝、粉、黄三个叠印的圆，左上角一个套准标记。纯装饰，读屏不读、不挡点击，窄屏不显示。
// 放在定位容器（页头）里；页头的文字排在它上面。
export function OverprintDeco({ className }: { className?: string }) {
  return (
    <div aria-hidden="true" data-overprint-deco className={cn('pointer-events-none absolute top-5 right-1.5 h-[132px] w-[300px] max-nav:hidden', className)}>
      <span className="absolute top-[34px] left-0 h-[84px] w-[150px] bg-[radial-gradient(var(--ink)_1.7px,transparent_2.1px)] bg-size-[9px_9px] opacity-55" />
      <span className="absolute top-2 left-[70px] size-24 rounded-full bg-riso-blue mix-blend-multiply" />
      <span className="absolute top-[26px] left-[132px] size-24 rounded-full bg-riso-pink mix-blend-multiply" />
      <span className="absolute top-2 left-[194px] size-24 rounded-full bg-riso-yellow mix-blend-multiply" />
      <svg width="18" height="18" viewBox="0 0 18 18" fill="none" stroke="var(--ink)" strokeWidth="1.2" className="absolute top-1 left-5">
        <circle cx="9" cy="9" r="5" />
        <path d="M9 0v18M0 9h18" />
      </svg>
    </div>
  )
}
