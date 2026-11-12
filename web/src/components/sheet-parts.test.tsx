import { render, screen, within } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { indexTabClass } from '@/lib/index-tab'
import { IndexTabLink, IndexTabs } from './IndexTabs'
import { HollowNumber, NumberedSection } from './NumberedSection'
import { RulerMark, RulerTicks, Sheet, Stub } from './Sheet'
import { StickyNote } from './StickyNote'

// 改版“一张印出来的纸”的共用部件：纸张、索引标签、票根与尺子、编号分区标题、黄色便签。
describe('纸张', () => {
  it('内容放在一张纸上：浅纸底、藏青描边、大圆角、孔版蓝实色投影，右侧给投影留出位置', () => {
    render(<Sheet data-testid="sheet">正文</Sheet>)
    const sheet = screen.getByTestId('sheet')
    expect(sheet).toHaveTextContent('正文')
    expect(sheet).toHaveAttribute('data-sheet')
    expect(sheet).toHaveClass('bg-paper-raised', 'border-2', 'border-ink', 'rounded-panel', 'shadow-print-dialog', 'overflow-hidden', 'mr-1.5')
    // 纸面上也有主区那层很淡的颗粒。
    expect(sheet).toHaveClass('riso-grain')
  })

  it('调用方可以追加类名与属性', () => {
    render(<Sheet data-testid="sheet" className="flex-1" aria-busy="true" />)
    expect(screen.getByTestId('sheet')).toHaveClass('flex-1', 'bg-paper-raised')
    expect(screen.getByTestId('sheet')).toHaveAttribute('aria-busy', 'true')
  })
})

describe('索引标签', () => {
  const tabs = (path: string) =>
    render(
      <MemoryRouter initialEntries={[path]}>
        <IndexTabs aria-label="开发助手">
          <IndexTabLink to="/chat">对话</IndexTabLink>
          <IndexTabLink to="/config">配置</IndexTabLink>
        </IndexTabs>
      </MemoryRouter>,
    )

  it('是一组带名称的导航链接，当前页标出来', () => {
    tabs('/config')
    const nav = screen.getByRole('navigation', { name: '开发助手' })
    expect(within(nav).getAllByRole('link').map((link) => link.textContent)).toEqual(['对话', '配置'])
    expect(within(nav).getByRole('link', { name: '配置' })).toHaveAttribute('aria-current', 'page')
    expect(within(nav).getByRole('link', { name: '对话' })).not.toHaveAttribute('aria-current')
  })

  it('当前页是黄色、高 40px；其余是米灰色、高 34px；都只有上、左、右三边描边', () => {
    tabs('/chat')
    const current = screen.getByRole('link', { name: '对话' })
    const other = screen.getByRole('link', { name: '配置' })
    expect(current).toHaveClass('h-10', 'bg-riso-yellow', 'text-ink', 'border-2', 'border-b-0', 'border-ink')
    expect(other).toHaveClass('h-[34px]', 'bg-paper-tab', 'text-ink-700', 'border-2', 'border-b-0', 'border-ink')
  })

  it('按钮式的分页（状态筛选、分类）用同一套样式', () => {
    expect(indexTabClass(true)).toContain('bg-riso-yellow')
    expect(indexTabClass(false)).toContain('bg-paper-tab')
    expect(indexTabClass(true)).not.toContain('bg-paper-tab')
  })
})

describe('票根与尺子', () => {
  it('票根有一个小标题，用虚线和正文隔开，窄屏不显示', () => {
    render(
      <Stub label="概况" data-testid="stub">
        <span>4 个文件</span>
      </Stub>,
    )
    const stub = screen.getByTestId('stub')
    expect(stub).toHaveTextContent('概况4 个文件')
    expect(stub).toHaveClass('border-l-2', 'border-dashed', 'max-nav:hidden')
    expect(screen.getByText('概况')).not.toHaveAttribute('aria-hidden')
  })

  it('票根作为导航区时用 aria-label 命名，可见的小标题不再重复读出', () => {
    render(
      <Stub as="nav" aria-label="对话轮次" label="轮次">
        <button type="button">第一轮</button>
      </Stub>,
    )
    const nav = screen.getByRole('navigation', { name: '对话轮次' })
    expect(within(nav).getByText('轮次')).toHaveAttribute('aria-hidden', 'true')
    expect(within(nav).getByRole('button', { name: '第一轮' })).toBeInTheDocument()
  })

  it('尺子的刻度和记号笔都是装饰，读屏不读', () => {
    render(
      <>
        <RulerTicks data-testid="ticks" />
        <RulerMark data-testid="mark" style={{ top: 12, height: 40 }} />
      </>,
    )
    expect(screen.getByTestId('ticks')).toHaveAttribute('aria-hidden', 'true')
    const mark = screen.getByTestId('mark')
    expect(mark).toHaveAttribute('aria-hidden', 'true')
    expect(mark).toHaveStyle({ top: '12px', height: '40px' })
    // 一笔黄色记号笔，左侧一个粉色小箭头。
    expect(mark.querySelector('[data-ruler-band]')).toHaveClass('bg-riso-yellow')
    expect(mark.querySelector('[data-ruler-arrow]')).toHaveClass('border-l-riso-pink')
  })
})

describe('编号分区', () => {
  it('空心编号是装饰，窄屏不显示', () => {
    render(<HollowNumber data-testid="number">25</HollowNumber>)
    const number = screen.getByTestId('number')
    expect(number).toHaveTextContent('25')
    expect(number).toHaveAttribute('aria-hidden', 'true')
    expect(number).toHaveClass('max-nav:hidden', 'text-transparent')
  })

  it('分区是一个以标题命名的区域：左侧两位编号，标题是二级标题加一笔黄色记号笔', () => {
    render(
      <NumberedSection number={3} title="本机目录">
        <p>分区内容</p>
      </NumberedSection>,
    )
    const region = screen.getByRole('region', { name: '本机目录' })
    expect(within(region).getByRole('heading', { level: 2, name: '本机目录' })).toHaveClass('font-display-cn', 'riso-marker')
    expect(within(region).getByText('03')).toHaveAttribute('aria-hidden', 'true')
    expect(region).toHaveTextContent('分区内容')
  })
})

describe('黄色便签', () => {
  it('是一条说明（role=note），带一个装饰用的信息图标', () => {
    render(<StickyNote>修改从下一条消息开始生效。</StickyNote>)
    const note = screen.getByRole('note')
    expect(note).toHaveTextContent('修改从下一条消息开始生效。')
    expect(note).toHaveClass('riso-tilt')
    expect(note.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  })
})
