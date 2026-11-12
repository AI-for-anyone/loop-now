import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { LiveDot, PageFrame, PageHeader } from './PageHeader'

// 改版“一张印出来的纸”第 3 步：项目与全局页面共用的页面标题（不是 Agent 页头）。
describe('页面标题', () => {
  const renderHeader = (meta?: React.ReactNode) =>
    render(
      <MemoryRouter>
        <PageHeader crumbs={[{ label: '示例项目', to: '/?project=示例项目' }]} title="任务">
          {meta}
        </PageHeader>
      </MemoryRouter>,
    )

  it('面包屑：前面的层级可以点，最后一项是当前页面，用孔版蓝标出', () => {
    renderHeader()
    const crumbs = screen.getByRole('navigation', { name: '页面位置' })
    expect(within(crumbs).getByRole('link', { name: '示例项目' })).toHaveAttribute('href', '/?project=示例项目')
    const current = within(crumbs).getByText('任务')
    expect(current).toHaveAttribute('aria-current', 'page')
    expect(current).toHaveClass('text-riso-blue')
  })

  it('标题是 ZCOOL 展示字，孔版蓝加荧光粉错版；宽屏紧凑（40px，与 Agent 页头一致），窄屏照旧最大 60px', () => {
    renderHeader()
    const title = screen.getByRole('heading', { level: 1, name: '任务' })
    expect(title).toHaveClass('font-display-cn', 'text-riso-blue', 'text-[clamp(38px,6vw,60px)]', '[text-shadow:4px_4px_0_var(--riso-pink)]')
    expect(title).toHaveClass('nav:text-[40px]', 'nav:leading-none', 'nav:[text-shadow:3px_3px_0_var(--riso-pink)]')
  })

  it('宽屏时页头紧凑：元信息并到标题右边一行，放不下时换行；面包屑更紧；装饰缩到和页头同高', () => {
    renderHeader(<span>本项目的任务</span>)
    const header = screen.getByRole('banner')
    expect(header).toHaveClass('nav:gap-1.5', 'nav:pt-3', 'nav:pb-2.5')
    const row = header.querySelector('[data-page-title-row]')!
    expect(row).toContainElement(screen.getByRole('heading', { level: 1 }))
    expect(row).toContainElement(screen.getByText('本项目的任务'))
    expect(row).toHaveClass('flex-col', 'nav:flex-row', 'nav:flex-wrap', 'nav:items-center', '@[820px]:pr-[316px]', 'nav:@[820px]:pr-[196px]')
    expect(within(header).getByRole('navigation', { name: '页面位置' }).querySelector('ol')).toHaveClass('nav:leading-4')
    expect(header.querySelector('[data-overprint-deco]')).toHaveClass('nav:scale-[0.6]')
  })

  it('标题下一行是元信息；右上角是叠印装饰，读屏不读，页头窄时不显示', () => {
    renderHeader(<span>本项目的任务</span>)
    const header = screen.getByRole('banner')
    expect(header).toHaveClass('@container', 'relative')
    expect(within(header).getByText('本项目的任务').closest('[data-page-meta]')).not.toBeNull()
    const deco = header.querySelector('[data-overprint-deco]')
    expect(deco).toHaveAttribute('aria-hidden', 'true')
    expect(deco).toHaveClass('hidden', '@[820px]:block')
  })

  it('没有元信息时不留空行', () => {
    renderHeader()
    expect(document.querySelector('[data-page-meta]')).toBeNull()
  })

  it('页面外框：左右与底部的留白随窗口宽度变化，与 Agent 页相同', () => {
    render(<PageFrame data-testid="frame">正文</PageFrame>)
    expect(screen.getByTestId('frame')).toHaveClass('flex', 'h-full', 'min-h-0', 'flex-col', 'px-[clamp(14px,2.4vw,34px)]', 'pb-[26px]')
  })
})

describe('实时状态', () => {
  it('连上时是绿点加“实时更新中”一类的说明；断开时是红点加正在重连', () => {
    const { rerender } = render(<LiveDot state="open" label="实时更新中" />)
    const status = screen.getByRole('status')
    expect(status).toHaveTextContent('实时更新中')
    expect(status).toHaveClass('text-green-text')
    expect(status.querySelector('[aria-hidden="true"]')).toHaveClass('bg-green')
    rerender(<LiveDot state="reconnecting" label="实时更新中" />)
    expect(screen.getByRole('status')).toHaveTextContent('正在重连')
    expect(screen.getByRole('status')).toHaveClass('text-red-text')
  })

  it('还在连接时只占位，不闪一下红色', () => {
    render(<LiveDot state="connecting" label="实时更新中" />)
    expect(screen.getByRole('status')).toBeEmptyDOMElement()
  })
})
