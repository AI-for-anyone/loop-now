import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { apiUrl, errorBody, makeMessage, makeRun } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { server } from '@/test/server'
import { Composer } from './Composer'
import { applyCommand, matchCommands, slashToken } from './slash-commands'

const command = (name: string, overrides: Partial<Schemas['AgentCommand']> = {}): Schemas['AgentCommand'] => ({ name, source: 'pi', piSource: 'extension', description: null, pluginId: null, pluginDisplayName: null, ...overrides })
const list: Schemas['AgentCommandList'] = {
  capturedAt: '2026-11-10T08:00:00Z',
  commands: [
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

const options = () => within(screen.getByRole('listbox', { name: 'Pi 命令' })).getAllByRole('option')
const labels = () => options().map((option) => option.querySelector('code')?.textContent)

describe('输入框的 "/" 命令菜单', () => {
  it('输入 "/" 才读取命令列表；每行是 /pi:<name>、来源、插件与说明（扩展原文按纯文本显示）；输入框指向选中的一项', async () => {
    const { user, input, reads } = setup()
    await user.type(input, '你好')
    expect(screen.queryByRole('listbox')).toBeNull()
    expect(reads()).toBe(0)
    await user.clear(input)
    await user.type(input, '/')
    await waitFor(() => expect(labels()).toEqual(['/pi:review', '/pi:skill:loopctl', '/pi:summarize']))
    expect(reads()).toBe(1)
    expect(options()[0]).toHaveTextContent('/pi:review扩展命令 · 插件 · 代码审查审查当前改动')
    expect(options()[1]).toHaveTextContent('/pi:skill:loopctl技能用 loopctl 管理 Agent <b>与任务</b>')
    expect(options()[2]).toHaveTextContent('/pi:summarize提示模板把对话写成摘要')
    expect(options()[0]).toHaveAttribute('aria-selected', 'true')
    expect(input).toHaveAttribute('aria-activedescendant', options()[0].id)
    expect(input).toHaveAttribute('aria-controls', screen.getByRole('listbox').id)
    expect(input).toHaveFocus()
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
    await user.type(input, '/')
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
    await user.type(input, '/sum')
    await waitFor(() => expect(options()).toHaveLength(1))
    await user.keyboard('{Tab}')
    expect(input).toHaveValue('/pi:summarize ')
    await user.clear(input)
    await user.type(input, '/r 第二段')
    expect(screen.queryByRole('listbox')).toBeNull()
    await user.keyboard('{Home}{ArrowRight}{ArrowRight}')
    await waitFor(() => expect(labels()).toEqual(['/pi:review', '/pi:summarize']))
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
    const { user, input, sent } = setup(() => HttpResponse.json({ capturedAt: null, commands: [] }))
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
    expect(applyCommand('/re', 'review')).toEqual({ text: '/pi:review ', caret: 11 })
    expect(applyCommand('/re\n第二行', 'review')).toEqual({ text: '/pi:review\n第二行', caret: 11 })
  })

  it('matchCommands：大小写不敏感，同一档保持原顺序', () => {
    expect(matchCommands(list.commands, '/PI:').map((item) => item.name)).toEqual(['review', 'skill:loopctl', 'summarize'])
    expect(matchCommands(list.commands, '/loop').map((item) => item.name)).toEqual(['skill:loopctl'])
  })
})
