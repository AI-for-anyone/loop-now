import { Fragment, type ComponentProps, type ReactNode } from 'react'
import { Link } from 'react-router'
import { OverprintDeco } from '@/components/OverprintDeco'
import { t } from '@/i18n'
import { sheetTitleClass } from '@/lib/page-title'
import { cn } from '@/lib/utils'

export interface Crumb {
  label: string
  // 没有地址的一层只显示文字（例如概览页上它自己的范围）。
  to?: string
}

// 页面外框：页头在上，纸张在下。左右与底部的留白随窗口宽度变化，底部给纸张的投影留出位置（与 Agent 页相同）。
export function PageFrame({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('flex h-full min-h-0 flex-col px-[clamp(14px,2.4vw,34px)] pb-[26px] max-nav:pb-4', className)} {...props} />
}

// 页面标题（改版通用规则第 3 条）：面包屑、ZCOOL 标题（孔版蓝加荧光粉错版）、一行元信息，右上角叠印装饰。
// 用于项目与全局的页面（概览、任务、项目设置、项目文件夹……）；Agent 详情有自己的页头（名称、徽标、地址）。
// 面包屑最后一项是当前页面，就是标题本身。页头是尺寸容器：宽于 820px 时显示装饰，各行文字给它让出位置。
// 宽屏时与 Agent 页头一样紧凑：标题 40px，元信息并到标题右边一行（放不下时换行），装饰缩到和页头同高；窄屏照旧。
export function PageHeader({ crumbs, title, children, className }: { crumbs: Crumb[]; title: string; children?: ReactNode; className?: string }) {
  return (
    <header data-page-header className={cn('@container relative flex shrink-0 flex-col gap-3 pt-[22px] pb-[18px] nav:gap-1.5 nav:pt-3 nav:pb-2.5', className)}>
      <OverprintDeco compact />
      <nav aria-label={t.shell.breadcrumb} className="relative @[820px]:pr-[316px] nav:@[820px]:pr-[196px]">
        <ol className="flex min-w-0 flex-wrap items-center gap-x-1.5 text-[12px] font-bold text-ink-500 nav:leading-4">
          {crumbs.map((crumb, index) => (
            <Fragment key={index}>
              <li className="min-w-0 [overflow-wrap:anywhere]">
                {crumb.to ? (
                  <Link to={crumb.to} className="hover:text-ink hover:underline">
                    {crumb.label}
                  </Link>
                ) : (
                  crumb.label
                )}
              </li>
              <li aria-hidden="true">›</li>
            </Fragment>
          ))}
          <li aria-current="page" className="min-w-0 [overflow-wrap:anywhere] text-riso-blue">
            {title}
          </li>
        </ol>
      </nav>
      <div
        data-page-title-row
        className="relative flex min-w-0 flex-col gap-3 @[820px]:pr-[316px] nav:flex-row nav:flex-wrap nav:items-center nav:gap-x-4 nav:gap-y-1.5 nav:@[820px]:pr-[196px]"
      >
        <h1 className={cn(sheetTitleClass, 'self-start nav:self-auto nav:text-[40px] nav:leading-none nav:[text-shadow:3px_3px_0_var(--riso-pink)]')}>{title}</h1>
        {children && (
          <div data-page-meta className="flex min-h-[26px] min-w-0 flex-wrap items-center gap-x-3.5 gap-y-2 text-[12.5px] text-ink-700">
            {children}
          </div>
        )}
      </div>
    </header>
  )
}

// 页头元信息里的实时状态：连上时绿点加说明（如“实时更新中”），断开时红点加“正在重连”；还在连接时只占位，不闪一下红色。
export function LiveDot({ state, label }: { state: 'connecting' | 'open' | 'reconnecting' | 'closed'; label: string }) {
  if (state === 'connecting') return <span role="status" aria-live="polite" />
  const open = state === 'open'
  return (
    <span role="status" aria-live="polite" className={cn('inline-flex items-center gap-1.5 text-[12px] font-black', open ? 'text-green-text' : 'text-red-text')}>
      <span aria-hidden="true" className={cn('size-[7px] shrink-0 rounded-full', open ? 'bg-green' : 'bg-red')} />
      {open ? label : t.chat.connection.reconnecting}
    </span>
  )
}
