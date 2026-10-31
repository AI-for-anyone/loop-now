import { render, screen, waitFor, within } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { buildSteps } from '@/features/chat/timeline-steps'
import { TimelineSteps } from '@/features/chat/TimelineBody'
import { InboxReminderLine } from '@/features/chat/InboxReminder'
import { partyDisplay } from '@/features/inbox/participants'
import { apiUrl } from '@/test/api'
import { renderChat } from '@/test/chat'

const pluginParty = (overrides: Partial<Schemas['InboxParticipant']> = {}): Schemas['InboxParticipant'] => ({
  kind: 'plugin',
  id: null,
  address: 'plugin:lark-notify/lark',
  name: '飞书通知',
  deleted: false,
  pluginId: 'lark-notify',
  channelId: 'lark',
  ...overrides,
})

describe('对话里的 / 命令', () => {
  it('被插件认领的命令：插件的回复排成一行说明，不算发出的消息，输入框清空', async () => {
    const { user } = await renderChat({
      handlers: [http.post(apiUrl('/agents/:agentId/messages'), () => HttpResponse.json({ handled: true, source: 'plugin', command: '', plugin: 'webhook', reply: '最近 24 小时送出 9 个事件，失败 0。' }))],
    })
    const box = screen.getByRole('textbox', { name: '消息' })
    await user.type(box, '/webhook status')
    await user.click(screen.getByRole('button', { name: '发送' }))
    const command = await waitFor(() => document.querySelector('[data-console-command]') as HTMLElement)
    expect(command).toHaveTextContent('/webhook status')
    expect(command).toHaveTextContent('插件 webhook 处理了这条命令')
    expect(command).toHaveTextContent('最近 24 小时送出 9 个事件，失败 0。')
    expect(box).toHaveValue('')
    // 没有变成一条“你发的”消息气泡：命令文字只出现在这一行说明里。
    const shown = within(screen.getByRole('log')).getAllByText('/webhook status')
    expect(shown).toHaveLength(1)
    expect(shown[0].closest('[data-console-command]')).toBe(command)
  })
})

describe('插件渠道来的发件人', () => {
  it('标题写成“插件 · 名称”，已卸载时标出', () => {
    expect(partyDisplay(pluginParty())).toEqual({ title: '插件 · 飞书通知', deleted: null, address: 'plugin:lark-notify/lark' })
    expect(partyDisplay(pluginParty({ deleted: true, name: '已卸载插件' }))).toEqual({ title: '插件 · lark-notify', deleted: '已卸载', address: 'plugin:lark-notify/lark' })
  })

  it('平台提醒一行里用小标签，不当成外部身份', () => {
    render(<InboxReminderLine at="2026-10-27T04:41:00Z" sender={pluginParty()} preview="飞书群里有人 @ 了你" />)
    const note = screen.getByRole('note')
    expect(within(note).getByText('插件 · 飞书通知').parentElement).toHaveAttribute('title', 'plugin:lark-notify/lark')
    expect(note).not.toHaveTextContent('来自')
  })
})

describe('运行记录里的扩展请求', () => {
  const entry = (overrides: Partial<Extract<Schemas['TimelineEntry'], { type: 'extension_ui' }>>): Schemas['TimelineEntry'] => ({
    type: 'extension_ui',
    at: '2026-10-27T04:41:00Z',
    method: 'notify',
    message: null,
    level: null,
    statusKey: null,
    answered: null,
    questionId: null,
    ...overrides,
  })

  it('通知与状态是淡色的一步（警告、错误带颜色）；对话类写明已回答、已过期或已取消，转成问题的带问题 ID', () => {
    const steps = buildSteps(null, [
      entry({ method: 'notify', message: '浏览器已打开', level: 'info' }),
      entry({ method: 'notify', message: '登录失效', level: 'error' }),
      entry({ method: 'setStatus', statusKey: 'browser', message: '正在加载 example.com' }),
      entry({ method: 'setStatus', statusKey: 'browser', message: null }),
      entry({ method: 'confirm', message: '要关闭所有标签页吗？', answered: 'answered', questionId: '76000000-0000-7000-8000-000000000001' }),
      entry({ method: 'select', message: '选一个分支', answered: 'expired', questionId: '76000000-0000-7000-8000-000000000002' }),
      entry({ method: 'editor', message: '编辑提交说明', answered: 'cancelled' }),
    ]).filter((step) => step.key.endsWith('extension_ui'))
    expect(steps.map((s) => s.questionId ?? null)).toEqual([null, null, null, null, '76000000-0000-7000-8000-000000000001', '76000000-0000-7000-8000-000000000002', null])
    expect(steps.map((s) => [s.label, s.field, s.detail, s.lines, s.tone])).toEqual([
      ['扩展通知', '提示', '浏览器已打开', [], 'neutral'],
      ['扩展通知', '错误', '登录失效', [], 'fail'],
      ['扩展状态', 'browser', '正在加载 example.com', [], 'neutral'],
      ['扩展状态', 'browser', '已清除', [], 'neutral'],
      ['扩展请求对话', 'confirm', '要关闭所有标签页吗？', ['已回答'], 'neutral'],
      ['扩展请求对话', 'select', '选一个分支', ['已过期：到期前没有回答，或这一轮先结束了'], 'neutral'],
      ['扩展请求对话', 'editor', '编辑提交说明', ['已取消：这种对话不能转成问题，已按取消回应'], 'neutral'],
    ])
  })

  it('转成问题的对话请求带“查看问题”，点了跳到对话里的那个问题', async () => {
    const steps = buildSteps(null, [entry({ method: 'confirm', message: '要关闭所有标签页吗？', answered: 'answered', questionId: '76000000-0000-7000-8000-000000000001' })])
    render(
      <>
        <div data-console-message-id="76000000-0000-7000-8000-000000000001">原问题</div>
        <TimelineSteps steps={steps} />
      </>,
    )
    const target = screen.getByText('原问题')
    const scroll = vi.fn()
    target.scrollIntoView = scroll
    screen.getByRole('button', { name: '查看问题' }).click()
    expect(scroll).toHaveBeenCalledWith({ block: 'center', behavior: 'smooth' })
  })
})
