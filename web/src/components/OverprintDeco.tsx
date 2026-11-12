import { cn } from '@/lib/utils'

// 页头右上角的装饰：一片网点，蓝、粉、黄三个叠印的圆，左上角一个套准标记。纯装饰，读屏不读、不挡点击。
// 放在页头里：页头要是定位容器，也要是尺寸容器（@container）。页头不到 820px 宽时不显示（窄屏、半屏窗口）；
// 显示时页头里的各行文字用 @[820px]:pr-[316px] 让出它的位置，长名称换行而不是压在圆上。
export function OverprintDeco({ className }: { className?: string }) {
  return (
    <div aria-hidden="true" data-overprint-deco className={cn('pointer-events-none absolute top-5 right-1.5 hidden h-[132px] w-[300px] @[820px]:block', className)}>
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
