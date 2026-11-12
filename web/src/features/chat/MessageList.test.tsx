import type { ComponentProps, ReactElement, ReactNode } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render as rtlRender, screen, within } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { MessagePages } from '@/api/queries'
import { makeAgent, makeMessage, makeMessagePage, makeRun } from '@/test/api'
import { createTestQueryClient } from '@/test/render'
import { initialLiveState, liveReducer, type LiveState } from './live-state'
import { MessageList } from './MessageList'
import { buildTurns } from './turns'

// 回复下方的运行记录读取查询缓存，渲染时需要 QueryClient。
function render(ui: ReactElement) {
  const client = createTestQueryClient()
  return rtlRender(ui, { wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> })
}

// jsdom 没有布局：对话区的内容高度与可视高度由测试设定，尺寸变化由测试触发。
const metrics = { scrollHeight: 0, clientHeight: 0 }

class FakeResizeObserver {
  static instances: FakeResizeObserver[] = []
  readonly targets = new Set<Element>()
  readonly callback: ResizeObserverCallback

  constructor(callback: ResizeObserverCallback) {
    this.callback = callback
    FakeResizeObserver.instances.push(this)
  }

  observe(target: Element) {
    this.targets.add(target)
  }

  unobserve(target: Element) {
    this.targets.delete(target)
  }

  disconnect() {
    this.targets.clear()
  }

  static resize(target: Element) {
    for (const observer of FakeResizeObserver.instances) {
      if (observer.targets.has(target)) observer.callback([], observer as unknown as ResizeObserver)
    }
  }
}

beforeEach(() => {
  FakeResizeObserver.instances = []
  vi.stubGlobal('ResizeObserver', FakeResizeObserver)
  metrics.scrollHeight = 1000
  metrics.clientHeight = 500
  for (const name of ['scrollHeight', 'clientHeight'] as const) {
    Object.defineProperty(HTMLElement.prototype, name, {
      configurable: true,
      get(this: HTMLElement) {
        return this.getAttribute('role') === 'log' ? metrics[name] : 0
      },
    })
  }
})

afterEach(() => {
  vi.unstubAllGlobals()
  Reflect.deleteProperty(HTMLElement.prototype, 'scrollHeight')
  Reflect.deleteProperty(HTMLElement.prototype, 'clientHeight')
  Reflect.deleteProperty(HTMLElement.prototype, 'scrollTop')
})

// 像浏览器一样把对话区的 scrollTop 限制在 0 到 scrollHeight - clientHeight 之间。
function clampScrollTop() {
  let top = 0
  Object.defineProperty(HTMLElement.prototype, 'scrollTop', {
    configurable: true,
    get(this: HTMLElement) {
      return this.getAttribute('role') === 'log' ? top : 0
    },
    set(this: HTMLElement, value: number) {
      if (this.getAttribute('role') === 'log') top = Math.max(0, Math.min(value, metrics.scrollHeight - metrics.clientHeight))
    },
  })
}

function pagesOf(...texts: string[]): MessagePages {
  const messages = texts.flatMap((text, i) => {
    const run = makeRun({ startedAt: `2026-10-12T08:00:0${i}Z` })
    return [makeMessage({ text, run }), makeMessage({ role: 'assistant', text: `${text}的回答`, run })]
  })
  return { pages: [makeMessagePage(messages, { hasMore: true })], pageParams: [null] }
}

const agent = makeAgent()

// 对话轮次由对话页用 buildTurns 合成后交给 MessageList（对话流与排队列表共用），这里照样合成。
function List({ pages, live, ...rest }: Omit<ComponentProps<typeof MessageList>, 'agent' | 'turns'> & { pages: MessagePages; live: LiveState }) {
  return <MessageList agent={agent} turns={buildTurns(pages, live).turns} {...rest} />
}

const props = { live: initialLiveState, hasOlder: true, loadingOlder: false, refreshing: false }

describe('对话区滚动', () => {
  // jsdom 不排版，只能确认滚动区是定位容器；整页不被撑高由浏览器截图检查（.local-build/chat-scroll-fix）。
  it('滚动区是定位容器，里面绝对定位的说明文字（未读圆点等）不会撑高整页', () => {
    render(<List {...props} pages={pagesOf('问题')} onLoadOlder={() => {}} />)
    expect(screen.getByRole('log')).toHaveClass('relative')
  })

  it('停在底部时，内容变高（图片加载完成、工具标记换行）也保持贴底', () => {
    render(<List {...props} pages={pagesOf('问题')} onLoadOlder={() => {}} />)
    const log = screen.getByRole('log')
    expect(log.scrollTop).toBe(1000)
    metrics.scrollHeight = 1300
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    expect(log.scrollTop).toBe(1300)
  })

  it('程序贴底后内容一下子变高很多，迟到的滚动事件不取消贴底', () => {
    clampScrollTop()
    render(<List {...props} pages={pagesOf('问题')} onLoadOlder={() => {}} />)
    const log = screen.getByRole('log')
    expect(log.scrollTop).toBe(500)
    // 回复结束时工具标记换行、最终文字出现，内容一次变高 300；程序贴底产生的滚动事件这时才到达。
    metrics.scrollHeight = 1300
    fireEvent.scroll(log)
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    expect(log.scrollTop).toBe(800)
  })

  it('人往上滚离开底部后不再贴底，滚回底部附近后恢复贴底', () => {
    clampScrollTop()
    render(<List {...props} pages={pagesOf('问题')} onLoadOlder={() => {}} />)
    const log = screen.getByRole('log')
    log.scrollTop = 300
    fireEvent.scroll(log)
    metrics.scrollHeight = 1300
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    expect(log.scrollTop).toBe(300)
    log.scrollTop = 760
    fireEvent.scroll(log)
    metrics.scrollHeight = 1600
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    expect(log.scrollTop).toBe(1100)
  })

  it('离开底部后，内容变高不改变位置', () => {
    render(<List {...props} pages={pagesOf('问题')} onLoadOlder={() => {}} />)
    const log = screen.getByRole('log')
    log.scrollTop = 200
    fireEvent.scroll(log)
    metrics.scrollHeight = 1300
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    expect(log.scrollTop).toBe(200)
  })

  it('加载更早的消息后，按开始加载时的内容高度补偿，保持原来看到的位置', () => {
    const onLoadOlder = vi.fn()
    const newer = pagesOf('新问题')
    const { rerender } = render(<List {...props} pages={newer} onLoadOlder={onLoadOlder} />)
    const log = screen.getByRole('log')
    log.scrollTop = 300
    fireEvent.scroll(log)
    // 视野内的图片加载完成，内容变高，但没有新的消息。
    metrics.scrollHeight = 1200
    act(() => FakeResizeObserver.resize(log.firstElementChild!))
    log.scrollTop = 0
    fireEvent.scroll(log)
    expect(onLoadOlder).toHaveBeenCalledTimes(1)
    rerender(<List {...props} loadingOlder pages={newer} onLoadOlder={onLoadOlder} />)
    // 更早的一页插在顶部，内容增加 500。
    const older = pagesOf('旧问题')
    metrics.scrollHeight = 1700
    rerender(<List {...props} pages={{ pages: [...newer.pages, ...older.pages], pageParams: [null, 'x'] }} onLoadOlder={onLoadOlder} />)
    expect(screen.getByText('旧问题')).toBeInTheDocument()
    expect(log.scrollTop).toBe(500)
  })

  it('整页重新获取进行中时不加载更早的消息', () => {
    const onLoadOlder = vi.fn()
    render(<List {...props} refreshing pages={pagesOf('问题')} onLoadOlder={onLoadOlder} />)
    const log = screen.getByRole('log')
    log.scrollTop = 0
    fireEvent.scroll(log)
    fireEvent.click(screen.getByRole('button', { name: '加载更早的消息' }))
    expect(onLoadOlder).not.toHaveBeenCalled()
  })
})

describe('回复的显示', () => {
  it('回复进行中显示输出光标与静态的状态文字，结束后不再显示光标', () => {
    const run = makeRun({ status: 'running', endedAt: null })
    const live: LiveState = { ...initialLiveState, current: { run, text: '写到一半' } }
    const history = (messages: ReturnType<typeof makeMessage>[]): MessagePages => ({ pages: [makeMessagePage(messages)], pageParams: [null] })
    const { container, rerender } = render(<List {...props} live={live} pages={history([makeMessage({ text: '问题', run })])} onLoadOlder={() => {}} />)
    expect(screen.getByText('写到一半')).toBeInTheDocument()
    expect(screen.getByText('回复中')).toBeInTheDocument()
    expect(container.querySelector('.riso-cursor')?.className).toContain('motion-reduce:animate-none')
    expect(container.querySelector('[class*="animate-spin"]')).toBeNull()

    const ended = { ...run, status: 'completed' as const, endedAt: '2026-10-12T08:00:30Z' }
    rerender(
      <List
        {...props}
        pages={history([makeMessage({ text: '问题', run: ended }), makeMessage({ role: 'assistant', text: '写完了', run: ended })])}
        onLoadOlder={() => {}}
      />,
    )
    expect(screen.getByText('写完了')).toBeInTheDocument()
    expect(screen.queryByText('回复中')).toBeNull()
    expect(container.querySelector('.riso-cursor')).toBeNull()
  })

  it('/pi: 扩展命令由 Pi 直接处理（piCommand.handled）且没有输出：用户消息照原文显示，回复处说明命令已执行、没有回复；展开成提示的命令照常显示回复', () => {
    const handled = makeRun({ startedAt: '2026-10-12T08:00:00Z', piCommand: { name: 'review', handled: true, notices: [], truncated: false } })
    const expanded = makeRun({ startedAt: '2026-10-12T08:00:01Z', piCommand: { name: 'skill:triage', handled: false, notices: [], truncated: false } })
    const plain = makeRun({ startedAt: '2026-10-12T08:00:02Z' })
    render(
      <List
        {...props}
        pages={{
          pages: [makeMessagePage([
            makeMessage({ text: '/pi:review src/', run: handled }), makeMessage({ role: 'assistant', text: '', run: handled }),
            makeMessage({ text: '/pi:skill:triage 看看', run: expanded }), makeMessage({ role: 'assistant', text: '分好类了', run: expanded }),
            makeMessage({ text: '普通问题', run: plain }), makeMessage({ role: 'assistant', text: '', run: plain }),
          ])],
          pageParams: [null],
        }}
        onLoadOlder={() => {}}
      />,
    )
    expect(screen.getByText('/pi:review src/')).toBeInTheDocument()
    expect(screen.getByText('/pi:skill:triage 看看')).toBeInTheDocument()
    expect(screen.getByText('分好类了')).toBeInTheDocument()
    expect(screen.getAllByText('命令已执行，没有回复')).toHaveLength(1)
  })

  it('扩展命令的输出（notices）按顺序显示成等宽纯文本，保留换行与空格；警告与错误写明级别；截断时说明；有输出就不说"没有回复"', () => {
    const usage = 'Weekly Limit (SuperGrok)\n   Used       1%\n   Reset      Nov 14, 04:41 EST'
    const run = makeRun({
      startedAt: '2026-10-12T08:00:00Z',
      piCommand: { name: 'grok-cli-usage', handled: true, truncated: true, notices: [{ level: 'info', message: usage }, { level: 'warning', message: '额度快用完了 <b>注意</b>' }, { level: 'error', message: '读取月度额度失败' }] },
    })
    const { container } = render(
      <List
        {...props}
        pages={{ pages: [makeMessagePage([makeMessage({ text: '/pi:grok-cli-usage', run }), makeMessage({ role: 'assistant', text: '', run })])], pageParams: [null] }}
        onLoadOlder={() => {}}
      />,
    )
    const blocks = [...container.querySelectorAll('[data-pi-command-notices] pre')]
    expect(blocks.map((block) => block.textContent)).toEqual([usage, '额度快用完了 <b>注意</b>', '读取月度额度失败'])
    expect(blocks[0].className).toContain('whitespace-pre-wrap')
    expect(blocks[0].className).toContain('font-mono')
    expect(blocks[1].parentElement).toHaveTextContent(/^警告/)
    expect(blocks[2].parentElement).toHaveTextContent(/^错误/)
    expect(blocks[0].parentElement).not.toHaveTextContent(/^(警告|错误)/)
    expect(container.querySelector('[data-pi-command-notices] b')).toBeNull()
    expect(screen.getByText('输出过长，已截断')).toBeInTheDocument()
    expect(screen.queryByText('命令已执行，没有回复')).toBeNull()
  })

  it('执行进行中没有输出；结束事件带来 notices 后一次显示，不用另取时间线', () => {
    const running = makeRun({ status: 'running', endedAt: null, piCommand: { name: 'grok-cli-usage', handled: true, notices: [], truncated: false } })
    const user = makeMessage({ text: '/pi:grok-cli-usage', run: running })
    const history: MessagePages = { pages: [makeMessagePage([user])], pageParams: [null] }
    const live: LiveState = { ...initialLiveState, current: { run: running, text: '' } }
    const { container, rerender } = render(<List {...props} live={live} pages={history} onLoadOlder={() => {}} />)
    expect(container.querySelector('[data-pi-command-notices]')).toBeNull()
    expect(screen.queryByText('命令已执行，没有回复')).toBeNull()
    const ended = { ...running, status: 'completed' as const, endedAt: '2026-10-12T08:00:30Z', piCommand: { name: 'grok-cli-usage', handled: true, notices: [{ level: 'info' as const, message: 'Used 1%' }], truncated: false } }
    const next = liveReducer(live, { type: 'event', event: { type: 'run.ended', id: '5-0', data: { run: ended, message: makeMessage({ role: 'assistant', text: '', run: ended }) } } })
    rerender(<List {...props} live={next} pages={history} onLoadOlder={() => {}} />)
    expect(container.querySelector('[data-pi-command-notices] pre')?.textContent).toBe('Used 1%')
  })

  it('打断消息在用户消息上方标明“打断”', () => {
    const run = makeRun({ whenBusy: 'interrupt' })
    render(
      <List
        {...props}
        pages={{ pages: [makeMessagePage([makeMessage({ text: '先停下', run }), makeMessage({ role: 'assistant', text: '好的', run })])], pageParams: [null] }}
        onLoadOlder={() => {}}
      />,
    )
    expect(screen.getByText('打断')).toBeInTheDocument()
  })

  it('被打断的那一轮：没结束的工具变灰标明已打断，随后一道“已打断，先处理这条”，打断消息的气泡为粉色', () => {
    const interrupted = makeRun({
      status: 'interrupted',
      endReason: 'user_interrupt',
      startedAt: '2026-10-12T08:00:00Z',
      endedAt: '2026-10-12T08:01:00Z',
      tools: [
        { toolCallId: 'read-1', toolName: 'read', state: 'started', at: '2026-10-12T08:00:01Z' },
        { toolCallId: 'read-1', toolName: 'read', state: 'succeeded', at: '2026-10-12T08:00:02Z' },
        { toolCallId: 'bash-1', toolName: 'bash', state: 'started', at: '2026-10-12T08:00:03Z' },
      ],
    })
    const interrupt = makeRun({ whenBusy: 'interrupt', startedAt: '2026-10-12T08:01:01Z' })
    const later = makeRun({ whenBusy: 'interrupt', startedAt: '2026-10-12T08:02:00Z' })
    const messages = [
      makeMessage({ text: '跑测试', run: interrupted }),
      makeMessage({ role: 'assistant', text: '先跑一遍', run: interrupted }),
      makeMessage({ text: '先跑 lint', run: interrupt }),
      makeMessage({ role: 'assistant', text: '好', run: interrupt }),
      makeMessage({ text: '上一轮正常结束', run: later }),
    ]
    const { container } = render(<List {...props} pages={{ pages: [makeMessagePage(messages)], pageParams: [null] }} onLoadOlder={() => {}} />)
    const tools = within(screen.getAllByRole('list', { name: '工具调用' })[0]).getAllByRole('listitem')
    expect(tools[0]).toHaveTextContent('read成功')
    expect(tools[1]).toHaveTextContent('bash已打断')
    expect(tools[1].querySelector('[data-interrupted]')).toHaveClass('border-dashed')
    expect(container.querySelector('[data-slot="tool-spinner"]')).toBeNull()
    // 只在被打断的那一轮之后出现一次；上一轮正常结束的打断消息前没有。
    const dividers = screen.getAllByRole('separator', { name: '已打断，先处理这条' })
    expect(dividers).toHaveLength(1)
    const bubble = screen.getByText('先跑 lint')
    expect(dividers[0].compareDocumentPosition(bubble) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    expect(bubble).toHaveClass('border-riso-pink', 'bg-tint-pink-2')
    expect(screen.getByText('跑测试')).toHaveClass('bg-tint-pink')
  })

  it('会话分隔线是带剪刀的粉色虚线裁切线，与“打断”分隔线同一种样式', () => {
    const before = makeRun({ status: 'interrupted', endReason: 'user_interrupt', startedAt: '2026-10-12T08:00:00Z', endedAt: '2026-10-12T08:01:00Z' })
    const interrupt = makeRun({ whenBusy: 'interrupt', startedAt: '2026-10-12T08:01:01Z' })
    const reopened = makeRun({ startedAt: '2026-10-12T09:00:00Z' })
    const messages = [
      makeMessage({ text: '第一问', run: before }),
      makeMessage({ text: '先做这个', run: interrupt }),
      makeMessage({ text: '重开后的问题', run: reopened, sessionEpoch: 2 }),
    ]
    render(<List {...props} pages={{ pages: [makeMessagePage(messages)], pageParams: [null] }} onLoadOlder={() => {}} />)
    const session = screen.getByRole('separator', { name: '会话已切换，页面历史保留' })
    const interrupted = screen.getByRole('separator', { name: '已打断，先处理这条' })
    expect(session).toHaveTextContent(/^会话已切换，页面历史保留$/)
    for (const divider of [session, interrupted]) {
      const lines = divider.querySelectorAll('[data-cut-line]')
      expect(lines).toHaveLength(2)
      for (const line of lines) expect(line).toHaveClass('border-t-2', 'border-dashed', 'border-riso-pink')
      expect(divider.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
    }
    // 裁切线左端是一把剪刀；打断分隔线的图标仍是闪电。
    expect(session.querySelector('svg')).toHaveClass('lucide-scissors')
    expect(interrupted.querySelector('svg')).toHaveClass('lucide-zap')
    expect(session.compareDocumentPosition(screen.getByText('重开后的问题')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('被 Inbox 唤醒的那一轮：发送者标明为平台提醒，与操作者发的消息区分开，随后是回复', () => {
    const run = makeRun()
    const reminder = makeMessage({ origin: 'inbox_reminder', text: '你有 2 条未读 Inbox 消息，运行 loopctl inbox list --unread 查看。', run })
    render(
      <List
        {...props}
        pages={{ pages: [makeMessagePage([reminder, makeMessage({ role: 'assistant', text: '我去看看', run })])], pageParams: [null] }}
        onLoadOlder={() => {}}
      />,
    )
    const note = screen.getByRole('note')
    expect(note).toHaveTextContent(/^平台提醒.+收到 Inbox 消息/)
    expect(note).not.toHaveTextContent('loopctl')
    expect(note).not.toHaveTextContent('你有 2 条未读')
    expect(screen.queryByText(/^你\s*·/)).toBeNull()
    expect(screen.getByText('我去看看')).toBeInTheDocument()
  })
})

it('sleep_wake 是一行淡色唤醒信息：时间、睡了多久与说明；平台指令和 sleep ID 不在正文里；提前唤醒的消息只带一个小标记', () => {
  const timer = '00000000-0000-7000-8000-000000005001', early = '00000000-0000-7000-8000-000000005002'
  const source = makeRun({ endedAt: '2026-10-26T08:00:00Z' }), wake = makeRun({ wakeSleepId: timer, endedAt: '2026-10-26T08:05:20Z' }), consoleRun = makeRun({ wakeSleepId: early })
  const pages: MessagePages = { pages: [makeMessagePage([
    makeMessage({ text: '每 5 分钟看一次 CI', run: source, createdAt: '2026-10-26T07:59:50Z' }),
    makeMessage({ role: 'assistant', text: '还没跑完，先 sleep', run: source, createdAt: '2026-10-26T07:59:55Z' }),
    makeMessage({ origin: 'sleep_wake', text: `Platform notice: your sleep is due; continue in the original conversation. sleepId=${timer}; note: check CI status for the budget fixture\nthen report`, run: wake, createdAt: '2026-10-26T08:05:03Z' }),
    makeMessage({ role: 'assistant', text: '已继续', run: wake, createdAt: '2026-10-26T08:05:04Z' }),
    makeMessage({ origin: 'console', text: '真实控制台留言', run: consoleRun, createdAt: '2026-10-26T08:06:00Z' }),
  ])], pageParams: [null] }
  render(<List {...props} pages={pages} onLoadOlder={() => {}} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(/^sleep 到期唤醒·.+·睡了 5 分钟·check CI status for the budget fixture then report$/u)
  expect(line).not.toHaveTextContent(/Platform notice|sleepId|5001/u)
  expect(line).toHaveAttribute('title', `check CI status for the budget fixture then report\nsleep ${timer}`)
  expect(line.className).not.toMatch(/riso-tilt|riso-yellow/)
  expect(screen.getByText('真实控制台留言')).toBeVisible()
  expect(screen.getByText('提前唤醒 sleep').closest('[data-early-wake]')).toHaveAttribute('title', `sleep ${early}`)
  expect(screen.queryByText(/唤醒关联/u)).toBeNull()
})

it('前一轮不是刚结束的那一轮时不写睡了多久；对不上格式的唤醒原文只放在悬停提示里', () => {
  const wake = makeRun({ wakeSleepId: 'sleep-timer' })
  const pages: MessagePages = { pages: [makeMessagePage([
    makeMessage({ origin: 'sleep_wake', text: '计时器提醒原文', run: wake }),
    makeMessage({ role: 'assistant', text: '已继续', run: wake }),
  ])], pageParams: [null] }
  render(<List {...props} pages={pages} onLoadOlder={() => {}} />)
  const line = screen.getByRole('note')
  expect(line).toHaveTextContent(/^sleep 到期唤醒·[^·]+$/u)
  expect(line).toHaveAttribute('title', '计时器提醒原文\nsleep sleep-timer')
})
