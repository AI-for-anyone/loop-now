import type { ComponentProps, ReactElement, ReactNode } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render as rtlRender, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import type { MessagePages } from '@/api/queries'
import { makeAgent, makeMessage, makeRun } from '@/test/api'
import { FakeIntersectionObserver } from '@/test/intersection-observer'
import { createTestQueryClient } from '@/test/render'
import { initialLiveState } from './live-state'
import { MessageList } from './MessageList'
import { turnPreview } from './ruler'
import { buildTurns, type Turn } from './turns'

// 回复下方的运行记录读取查询缓存，渲染时需要 QueryClient。
function render(ui: ReactElement) {
  const client = createTestQueryClient()
  return rtlRender(ui, { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
}

// jsdom 没有布局：对话区与刻度尺的尺寸由测试设定，并像浏览器一样把对话区的 scrollTop 限制在可滚动范围内。
const log = { scrollHeight: 2000, clientHeight: 500, top: 0 }
const ruler = { scrollHeight: 0, clientHeight: 0 }

beforeEach(() => {
  log.scrollHeight = 2000
  log.clientHeight = 500
  log.top = 0
  ruler.scrollHeight = 0
  ruler.clientHeight = 0
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  )
  for (const name of ['scrollHeight', 'clientHeight'] as const) {
    Object.defineProperty(HTMLElement.prototype, name, {
      configurable: true,
      get(this: HTMLElement) {
        if (this.getAttribute('role') === 'log') return log[name]
        return this.tagName === 'OL' ? ruler[name] : 0
      },
    })
  }
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get(this: HTMLElement) {
      return this.getAttribute('role') === 'log' ? log.top : Number(this.dataset.testScrollTop ?? 0)
    },
    set(this: HTMLElement, value: number) {
      if (this.getAttribute('role') === 'log') log.top = Math.max(0, Math.min(value, log.scrollHeight - log.clientHeight))
      else this.dataset.testScrollTop = String(value)
    },
  })
})

afterEach(() => {
  vi.unstubAllGlobals()
  for (const name of ['scrollHeight', 'clientHeight', 'scrollTop', 'offsetTop', 'offsetHeight']) Reflect.deleteProperty(HTMLElement.prototype, name)
})

function pagesOf(texts: string[], hasMore = true): MessagePages {
  const messages = texts.flatMap((text, i) => {
    const run = makeRun({ startedAt: new Date(Date.parse('2026-11-12T08:00:00Z') + i * 1000).toISOString() })
    return [makeMessage({ text, run }), makeMessage({ role: 'assistant', text: `${text}的回答`, run })]
  })
  return { pages: [{ messages, hasMore, hasMoreAfter: false }], pageParams: [null] }
}

const agent = makeAgent()
const props = { hasOlder: true, loadingOlder: false, refreshing: false, onLoadOlder: () => {} }

function List({ pages, ...rest }: Omit<ComponentProps<typeof MessageList>, 'agent' | 'turns' | 'hasOlder' | 'loadingOlder' | 'refreshing' | 'onLoadOlder'> &
  Partial<ComponentProps<typeof MessageList>> & { pages: MessagePages }) {
  return <MessageList agent={agent} turns={buildTurns(pages, initialLiveState).turns} {...props} {...rest} />
}

const nav = () => screen.getByRole('navigation', { name: '对话轮次' })
const ticks = () => within(nav()).getAllByRole('button')
const turnBlock = (text: string) => screen.getByText(text).closest<HTMLElement>('[data-run-id]')!

describe('对话区右侧的刻度尺', () => {
  it('一条刻度一轮（已开始的轮次），名称是用户消息的开头；排队中的消息不算；加载更早的消息后刻度增加', () => {
    const pages = pagesOf(['第一问', '第二问', '第三问'])
    const queuedRun = makeRun({ status: 'queued', startedAt: null, runningAt: null, endedAt: null })
    pages.pages[0].messages.push(makeMessage({ text: '排队中的问题', run: queuedRun }))
    const { rerender } = render(<List pages={pages} />)
    expect(ticks().map((tick) => tick.getAttribute('aria-label'))).toEqual(['第一问', '第二问', '第三问'])

    const older = pagesOf(['更早一', '更早二'])
    for (const [i, message] of older.pages[0].messages.entries()) message.run.startedAt = new Date(Date.parse('2026-11-11T08:00:00Z') + i).toISOString()
    rerender(<List pages={{ pages: [...pages.pages, ...older.pages], pageParams: [null, 'x'] }} />)
    expect(ticks().map((tick) => tick.getAttribute('aria-label'))).toEqual(['更早一', '更早二', '第一问', '第二问', '第三问'])
  })

  it('只有一轮或没有对话时不显示', () => {
    const { rerender } = render(<List pages={pagesOf(['唯一的问题'])} />)
    expect(screen.queryByRole('navigation')).toBeNull()
    rerender(<List pages={pagesOf([])} />)
    expect(screen.queryByRole('navigation')).toBeNull()
    rerender(<List pages={pagesOf(['一', '二'])} />)
    expect(ticks()).toHaveLength(2)
  })

  it('窄屏（左侧栏收进抽屉时）隐藏', () => {
    render(<List pages={pagesOf(['一', '二'])} />)
    expect(nav()).toHaveClass('max-nav:hidden')
  })

  it('对话区里看得到的那几轮，刻度标为当前并加深；滚动后跟着变', () => {
    render(<List pages={pagesOf(['一', '二', '三', '四'])} />)
    act(() => {
      FakeIntersectionObserver.trigger(turnBlock('三'), true)
      FakeIntersectionObserver.trigger(turnBlock('四'), true)
    })
    const current = () => ticks().filter((tick) => tick.getAttribute('aria-current') === 'true').map((tick) => tick.getAttribute('aria-label'))
    expect(current()).toEqual(['三', '四'])
    expect(within(nav()).getByRole('button', { name: '四' }).firstElementChild).toHaveClass('bg-ink-500')
    expect(within(nav()).getByRole('button', { name: '一' }).firstElementChild).toHaveClass('bg-border')
    act(() => {
      FakeIntersectionObserver.trigger(turnBlock('四'), false)
      FakeIntersectionObserver.trigger(turnBlock('二'), true)
    })
    expect(current()).toEqual(['二', '三'])
  })

  it('点击刻度跳到那一轮的开头；之后内容变高不被贴底拉回，跳转带来的滚动也不加载更早的消息', async () => {
    const onLoadOlder = vi.fn()
    const pages = pagesOf(['一', '二', '三'])
    const { rerender } = render(<List pages={pages} onLoadOlder={onLoadOlder} />)
    const el = screen.getByRole('log')
    expect(el.scrollTop).toBe(1500)
    // 第一轮的开头在对话区顶端下方 -1420px（在视野之上很远）。
    el.getBoundingClientRect = () => DOMRect.fromRect({ x: 0, y: 100, width: 600, height: 500 })
    turnBlock('一').getBoundingClientRect = () => DOMRect.fromRect({ x: 0, y: 100 - 1420, width: 600, height: 300 })
    await userEvent.setup().click(within(nav()).getByRole('button', { name: '一' }))
    expect(el.scrollTop).toBe(1500 - 1420 - 24)
    fireEvent.scroll(el)
    expect(onLoadOlder).not.toHaveBeenCalled()
    // 最后一轮的回复变长、内容变高：不贴底。
    log.scrollHeight = 2400
    const messages = pages.pages[0].messages
    rerender(<List pages={{ ...pages, pages: [{ ...pages.pages[0], messages: [...messages.slice(0, -1), { ...messages.at(-1)!, text: '三的回答，更长了' }] }] }} onLoadOlder={onLoadOlder} />)
    expect(screen.getByText('三的回答，更长了')).toBeInTheDocument()
    expect(el.scrollTop).toBe(56)
    // 之后人自己滚到顶部附近，照常加载更早的消息。
    el.scrollTop = 20
    fireEvent.scroll(el)
    expect(onLoadOlder).toHaveBeenCalledTimes(1)
  })

  it('定位模式下跳到最后一轮靠近底部，不因此加载更新的消息；回车也能跳转', async () => {
    const onLoadNewer = vi.fn()
    const pages = pagesOf(['一', '二', '三'])
    render(<List pages={pages} targetRunId={pages.pages[0].messages[0].run.id} hasNewer onLoadNewer={onLoadNewer} />)
    const el = screen.getByRole('log')
    el.getBoundingClientRect = () => DOMRect.fromRect({ x: 0, y: 0, width: 600, height: 500 })
    turnBlock('三').getBoundingClientRect = () => DOMRect.fromRect({ x: 0, y: 1800, width: 600, height: 200 })
    const user = userEvent.setup()
    within(nav()).getByRole('button', { name: '三' }).focus()
    await user.keyboard('{Enter}')
    expect(el.scrollTop).toBe(1500)
    fireEvent.scroll(el)
    expect(onLoadNewer).not.toHaveBeenCalled()
  })

  it('悬停或键盘聚焦刻度时在左侧显示这一轮的开头，移开后消失', async () => {
    render(<List pages={pagesOf(['帮我看看构建为什么失败', '再跑一次测试'])} />)
    const user = userEvent.setup()
    const preview = () => nav().querySelector('[data-ruler-preview]')
    await user.hover(within(nav()).getByRole('button', { name: '帮我看看构建为什么失败' }))
    expect(preview()).toHaveTextContent('帮我看看构建为什么失败')
    expect(preview()).toHaveAttribute('aria-hidden', 'true')
    await user.unhover(within(nav()).getByRole('button', { name: '帮我看看构建为什么失败' }))
    expect(preview()).toBeNull()
    act(() => within(nav()).getByRole('button', { name: '帮我看看构建为什么失败' }).focus())
    expect(preview()).toHaveTextContent('帮我看看构建为什么失败')
    await user.tab()
    expect(within(nav()).getByRole('button', { name: '再跑一次测试' })).toHaveFocus()
    expect(preview()).toHaveTextContent('再跑一次测试')
    await user.tab()
    expect(preview()).toBeNull()
    // 鼠标点击也会聚焦刻度，但移开鼠标后预览不留着。
    await user.click(within(nav()).getByRole('button', { name: '再跑一次测试' }))
    expect(within(nav()).getByRole('button', { name: '再跑一次测试' })).toHaveFocus()
    expect(preview()).toHaveTextContent('再跑一次测试')
    await user.unhover(within(nav()).getByRole('button', { name: '再跑一次测试' }))
    expect(preview()).toBeNull()
  })

  it('轮次多到刻度尺放不下时，刻度尺自己滚动，并让可见轮的刻度留在可视范围里', () => {
    // 60 轮压缩到最小间距 8px：刻度从 24px 开始排，刻度尺可视高度 200px。
    ruler.scrollHeight = 24 + 60 * 8 + 24
    ruler.clientHeight = 200
    Object.defineProperty(HTMLElement.prototype, 'offsetTop', {
      configurable: true,
      get(this: HTMLElement) {
        return this.tagName === 'LI' ? 24 + Array.prototype.indexOf.call(this.parentElement!.children, this) * 8 : 0
      },
    })
    Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
      configurable: true,
      get(this: HTMLElement) {
        return this.tagName === 'LI' ? 8 : 0
      },
    })
    const texts = Array.from({ length: 60 }, (_, i) => `问题${i + 1}`)
    render(<List pages={pagesOf(texts)} />)
    const list = nav().querySelector('ol')!
    act(() => {
      FakeIntersectionObserver.trigger(turnBlock('问题59'), true)
      FakeIntersectionObserver.trigger(turnBlock('问题60'), true)
    })
    // 第 59、60 轮的刻度在 24+58*8=488 到 24+60*8=504，下方留 16px：滚到 520-200=320。
    expect(list.scrollTop).toBe(320)
    act(() => {
      FakeIntersectionObserver.trigger(turnBlock('问题59'), false)
      FakeIntersectionObserver.trigger(turnBlock('问题60'), false)
      FakeIntersectionObserver.trigger(turnBlock('问题2'), true)
    })
    // 第 2 轮的刻度在 32，上方留 16px。
    expect(list.scrollTop).toBe(16)
  })
})

describe('刻度的名称', () => {
  const turn = (user: Partial<Schemas['Message']> | null, reply: string | null = null): Turn => ({
    key: 'k',
    user: user && makeMessage(user),
    run: makeRun(),
    reply: reply === null ? null : { text: reply, live: false },
    sessionEpoch: 1,
  })

  it('用户消息合并空白；太长时截断', () => {
    expect(turnPreview(turn({ text: '  第一行\n\n第二行\t结尾 ' }))).toBe('第一行 第二行 结尾')
    expect(turnPreview(turn({ text: '字'.repeat(100) }))).toBe(`${'字'.repeat(80)}…`)
  })

  it('只有图片时写图片张数；平台提醒与 sleep 唤醒写明是哪种', () => {
    const image: Schemas['MessageImage'] = { id: '00000000-0000-4000-8000-000000000001', mimeType: 'image/png', size: 1, width: 1, height: 1 }
    expect(turnPreview(turn({ text: '', images: [image, image] }))).toBe('2 张图片')
    expect(turnPreview(turn({ origin: 'inbox_reminder', text: '[platform] inbox' }))).toBe('平台提醒：收到 Inbox 消息')
    const wake = 'Platform notice: your sleep is due; continue in the original conversation. sleepId=0b0c'
    expect(turnPreview(turn({ origin: 'sleep_wake', text: `${wake}; note: 看看  构建\n结果` }))).toBe('sleep 到期唤醒：看看 构建 结果')
    expect(turnPreview(turn({ origin: 'sleep_wake', text: wake }))).toBe('sleep 到期唤醒')
  })

  it('没有用户消息时用回复的开头；回复也没有文字时用通用说法', () => {
    expect(turnPreview(turn(null, '构建已经修好了'))).toBe('构建已经修好了')
    expect(turnPreview(turn(null, ''))).toBe('没有文字的一轮')
    expect(turnPreview(turn({ text: '' }, null))).toBe('没有文字的一轮')
  })
})
