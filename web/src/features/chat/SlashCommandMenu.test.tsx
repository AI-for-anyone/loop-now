import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeMessage, makeRun } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { claudeCodeCommands, piCommand, platformCommands } from '@/test/commands'
import { server } from '@/test/server'
import { Composer } from './Composer'
import { applyCommand, commandText, matchCommands, slashToken } from './slash-commands'

const command = piCommand
const list: Schemas['AgentCommandList'] = {
  capturedAt: '2026-11-10T08:00:00Z',
  commands: [
    ...platformCommands(),
    command('review', { description: '审查当前改动', pluginId: 'reviewer', pluginDisplayName: { 'zh-CN': '代码审查', 'en-US': 'Code review' } }),
    command('skill:loopctl', { piSource: 'skill', description: '用 loopctl 管理 Agent <b>与任务</b>' }),
    command('summarize', { piSource: 'prompt', description: '把对话写成摘要' }),
  ],
}

// 对话页的输入框：记下读命令列表的次数与发出的消息。
function setup(commands: () => Response = () => HttpResponse.json(list)) {
  let reads = 0
  const sent: Schemas['SendMessageInput'][] = []
  server.use(
    http.get(apiUrl('/agents/agent-1/commands'), () => { reads += 1; return commands() }),
    http.post(apiUrl('/agents/agent-1/messages'), async ({ request }) => {
      const body = (await request.json()) as Schemas['SendMessageInput']
      sent.push(body)
      const run = makeRun({ status: 'starting', endedAt: null })
      return HttpResponse.json({ run, message: makeMessage({ text: body.text, run }) }, { status: 202 })
    }),
  )
  renderWithQuery(<Composer agentId="agent-1" agentName="开发助手" busy={false} disabled={false} maxMessageBytes={4096} onSent={() => {}} />)
  return { user: userEvent.setup(), input: screen.getByRole('textbox', { name: '消息' }), sent, reads: () => reads }
}

const options = () => within(screen.getByRole('listbox', { name: '命令' })).getAllByRole('option')
const labels = () => options().map((option) => option.querySelector('code')?.textContent)

describe('输入框的 "/" 命令菜单', () => {
  it('输入 "/" 才读取命令列表；每行是 /pi:<name>、来源、插件与说明（扩展原文按纯文本显示）；输入框指向选中的一项', async () => {
    const { user, input, reads } = setup()
    await user.type(input, '你好')
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(reads()).toBe(0)
    await user.clear(input)
    await user.type(input, '/')
    await waitFor(() => expect(labels()).toEqual(['/session', '/new', '/compact [instructions]', '/reload', '/copy', '/export [html|jsonl]', '/resume [sessionId]', '/pi:review', '/pi:skill:loopctl', '/pi:summarize']))
    expect(reads()).toBe(1)
    expect(options()[7]).toHaveTextContent('/pi:review扩展命令 · 插件 · 代码审查审查当前改动')
    expect(options()[8]).toHaveTextContent('/pi:skill:loopctl技能用 loopctl 管理 Agent <b>与任务</b>')
    expect(options()[9]).toHaveTextContent('/pi:summarize提示模板把对话写成摘要')
    expect(options()[0]).toHaveAttribute('aria-selected', 'true')
    expect(input).toHaveAttribute('aria-activedescendant', options()[0].id)
    expect(input).toHaveAttribute('aria-controls', screen.getByRole('listbox').id)
    expect(input).toHaveFocus()
  })

  // 改版“一张印出来的纸”：菜单是一小张纸，选中的一项是黄色，来源写在小标签里。
  it('菜单是一张小纸；选中的一项是黄色；来源写在标签里：平台命令藏青底白字，Pi 的命令浅蓝底', async () => {
    const { user, input } = setup()
    await user.type(input, '/')
    await waitFor(() => expect(options()).toHaveLength(10))
    const panel = screen.getByRole('listbox', { name: '命令' }).closest<HTMLElement>('[data-radix-popper-content-wrapper] > *')!
    expect(panel).toHaveClass('bg-paper-raised', 'rounded-card', 'shadow-[0_0_0_2px_var(--ink),4px_4px_0_2px_var(--riso-blue)]')
    for (const option of options()) expect(option).toHaveClass('aria-selected:bg-riso-yellow')
    expect(options()[0]).not.toHaveClass('aria-selected:bg-tint-blue')
    expect(within(options()[0]).getByText('平台命令')).toHaveClass('rounded-full', 'bg-ink', 'text-white')
    expect(within(options()[7]).getByText('扩展命令 · 插件 · 代码审查')).toHaveClass('rounded-stamp', 'bg-tint-blue', 'text-ink')
    // 底部的按键说明与列表之间是一道虚线。
    expect(panel.lastElementChild).toHaveClass('border-t-[1.5px]', 'border-dashed')
  })

  it('按第一个词筛选：/pi: 写法开头的最前，其次是没写 pi: 的命令名开头，再次是名称或说明包含', async () => {
    const { user, input } = setup()
    await user.type(input, '/pi:s')
    await waitFor(() => expect(labels()).toEqual(['/pi:skill:loopctl', '/pi:summarize']))
    await user.clear(input)
    await user.type(input, '/rev')
    await waitFor(() => expect(labels()).toEqual(['/pi:review']))
    await user.clear(input)
    await user.type(input, '/摘要')
    await waitFor(() => expect(labels()).toEqual(['/pi:summarize']))
    await user.clear(input)
    await user.type(input, '/nothing')
    expect(await screen.findByRole('status')).toHaveTextContent('没有匹配的命令。')
  })

  it('上下键移动，Enter 补全成 "/pi:<name> " 不发送；接着写参数，回车照常发出原文', async () => {
    const { user, input, sent } = setup()
    await user.type(input, '/pi:')
    await waitFor(() => expect(options()).toHaveLength(3))
    await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}{ArrowUp}')
    expect(options()[1]).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{Enter}')
    expect(input).toHaveValue('/pi:skill:loopctl ')
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(sent).toEqual([])
    await user.type(input, '列出任务')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '/pi:skill:loopctl 列出任务' }]))
  })

  it('Tab 与点击同样补全；已经写了参数时只换掉第一个词', async () => {
    const { user, input } = setup()
    await user.type(input, '/pi:sum')
    await waitFor(() => expect(options()).toHaveLength(1))
    await user.keyboard('{Tab}')
    expect(input).toHaveValue('/pi:summarize ')
    await user.clear(input)
    await user.type(input, '/rev 第二段')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.keyboard('{Home}{ArrowRight}{ArrowRight}')
    await waitFor(() => expect(labels()).toEqual(['/pi:review']))
    await user.click(options()[0])
    expect(input).toHaveValue('/pi:review 第二段')
    expect(input).toHaveFocus()
  })

  it('Esc 关掉菜单，回车照原文发出；改了第一个词再打开', async () => {
    const { user, input, sent } = setup()
    await user.type(input, '/rev')
    await waitFor(() => expect(options()).toHaveLength(1))
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.type(input, 'i')
    await waitFor(() => expect(labels()).toEqual(['/pi:review']))
    await user.keyboard('{Escape}{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '/revi' }]))
  })

  it('Agent 还没运行过（capturedAt 为 null）：说明运行一次后才有列表，回车照常发出', async () => {
    const { user, input, sent } = setup(() => HttpResponse.json({ capturedAt: null, commands: platformCommands() }))
    await user.type(input, '/foo')
    expect(await screen.findByRole('status')).toHaveTextContent('Agent 运行一次后才有命令列表。')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '/foo' }]))
  })

  it('读不到命令列表时说明原因，不挡发送', async () => {
    const { user, input, sent } = setup(() => HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 }))
    await user.type(input, '/pi:review')
    expect(await screen.findByRole('status')).toHaveTextContent(/^读不到命令列表：服务端内部错误/)
    await user.keyboard('{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '/pi:review' }]))
  })

  it('Claude Code 的 Agent：没有 /pi: 命令；能用的平台命令照常提示，/compact、/export、/resume 列在“现在不能用”里并写明原因', async () => {
    const { user, input, sent } = setup(() => HttpResponse.json({ capturedAt: null, commands: claudeCodeCommands() }))
    await user.type(input, '/')
    await waitFor(() => expect(labels()).toEqual(['/session', '/new', '/reload', '/copy']))
    const blocked = screen.getByRole('group', { name: '现在不能用' })
    expect([...blocked.querySelectorAll('code')].map((code) => code.textContent)).toEqual(['/compact [instructions]', '/export [html|jsonl]', '/resume [sessionId]'])
    expect(within(blocked).getAllByText('这个 Agent 用 Claude Code 运行，没有 Pi 会话')).toHaveLength(3)
    expect(within(blocked).queryByRole('option')).toBeNull()
    // 没有匹配的命令时只说没有匹配，不说“运行一次后才有命令列表”（它没有 Pi，运行了也不会有）。
    await user.clear(input)
    await user.type(input, '/review')
    expect(await screen.findByRole('status')).toHaveTextContent('没有匹配的命令。')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.keyboard('{Enter}')
    await waitFor(() => expect(sent).toEqual([{ text: '/review' }]))
  })

  it('内置命令只认服务端给的列表：没列的不显示，多列的照样显示（没有说明就不写说明）', async () => {
    const [session] = platformCommands()
    const ping = { ...session, name: 'ping', usage: '/ping [host]' }
    const { user, input } = setup(() => HttpResponse.json({ capturedAt: null, commands: [session, ping] }))
    await user.type(input, '/')
    await waitFor(() => expect(labels()).toEqual(['/session', '/ping [host]']))
    expect(options()[0]).toHaveTextContent('/session平台命令查看当前会话、模型和进程状态')
    expect(options()[1]).toHaveTextContent(/^\/ping \[host\]平台命令$/)
    await user.keyboard('{ArrowDown}{Enter}')
    expect(input).toHaveValue('/ping')
  })

  it('不可用的命令置灰列在下面、写明原因、不能选；上下键和回车只在可用的命令之间走', async () => {
    const commands = platformCommands({ compact: { available: false, unavailableReason: 'session_none' }, export: { available: false, unavailableReason: 'session_missing' } })
    const { user, input } = setup(() => HttpResponse.json({ capturedAt: null, commands }))
    await user.type(input, '/')
    await waitFor(() => expect(labels()).toEqual(['/session', '/new', '/reload', '/copy', '/resume [sessionId]']))
    const blocked = screen.getByRole('group', { name: '现在不能用' })
    expect(blocked).toHaveTextContent('/compact [instructions]平台命令还没有会话，先发一条消息')
    expect(blocked).toHaveTextContent('/export [html|jsonl]平台命令会话文件丢失，先 /new 或 /resume')
    for (const item of blocked.querySelectorAll('li')) expect(item).toHaveClass('text-ink-500')
    // 点不可用的命令不填入输入框。
    await user.click(within(blocked).getByText('/compact [instructions]'))
    expect(input).toHaveValue('/')
    await user.keyboard('{ArrowDown}{ArrowDown}')
    expect(options()[2]).toHaveAttribute('aria-selected', 'true')
    expect(labels()[2]).toBe('/reload')
    // 只匹配到不可用的命令时没有可选项，回车照常发出，由服务端说明为什么不行。
    await user.clear(input)
    await user.type(input, '/comp')
    await waitFor(() => expect(screen.queryByRole('listbox')).toBeNull())
    expect(screen.getByRole('group', { name: '现在不能用' })).toHaveTextContent('/compact [instructions]')
    expect(input).not.toHaveAttribute('aria-activedescendant')    // 没有可选的命令：底部不再写“↑↓ 选择 · Enter 或 Tab 补全”，改成告诉人回车会照原文发出；读屏也会读到。
    expect(screen.queryByText('↑↓ 选择 · Enter 或 Tab 补全 · Esc 关闭')).toBeNull()
    expect(screen.getByRole('status')).toHaveTextContent('匹配的命令现在都不能用，回车会照原文发出')
  })

  it('只认开头的 "/"：前面有空格、不在开头、光标离开第一个词时都不弹菜单', async () => {
    const { user, input } = setup()
    await user.type(input, ' /pi:review')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.clear(input)
    await user.type(input, '看看 /pi:review')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.clear(input)
    await user.type(input, '/pi:review src')
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(screen.queryByRole('status')).toBeNull()
  })
})

describe('补全的规则', () => {
  it('slashToken：开头的 "/" 词，光标在词里或紧跟其后', () => {
    expect(slashToken('/pi:re', 6)).toBe('/pi:re')
    expect(slashToken('/pi:re arg', 3)).toBe('/pi:re')
    expect(slashToken('/pi:re arg', 7)).toBeNull()
    expect(slashToken(' /pi', 4)).toBeNull()
    expect(slashToken('', 0)).toBeNull()
  })

  it('applyCommand：换掉第一个词，保留后面的空白与文字；后面没有空白时补一个空格', () => {
    expect(applyCommand('/re', command('review'))).toEqual({ text: '/pi:review ', caret: 11 })
    expect(applyCommand('/re\n第二行', command('review'))).toEqual({ text: '/pi:review\n第二行', caret: 11 })
  })

  it('commandText：Pi 的命令写成 /pi:<name>；以后别的来源（如平台命令）写成 /<name>，补全与筛选随之', () => {
    expect(commandText(command('skill:loopctl'))).toBe('/pi:skill:loopctl')
    // 平台命令在前端与服务端返回的 Pi 命令合并。
    const platform = { ...command('session'), source: 'platform' as const, usage: '/session' }
    expect(commandText(platform)).toBe('/session')
    expect(applyCommand('/se', platform)).toEqual({ text: '/session ', caret: 9 })
    expect(matchCommands([command('session'), platform], '/se')).toEqual([platform, command('session')])
  })

  it('matchCommands：大小写不敏感，同一档保持原顺序', () => {
    expect(matchCommands(list.commands, '/PI:').map((item) => item.name)).toEqual(['review', 'skill:loopctl', 'summarize'])
    expect(matchCommands(list.commands, '/loop').map((item) => item.name)).toEqual(['skill:loopctl'])
  })
})
