// @vitest-environment-options {"url": "http://localhost:3000/?lang=en-US"}
import { screen, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/client'
import { locale } from '@/i18n/locale'
import { describeError } from '@/lib/errors'
import { defaultAgentPath, makeAgent } from '@/test/api'
import { renderChat } from '@/test/chat'
import { renderConfig } from '@/test/config'
import { makeAgentOverview, renderOverview } from '@/test/overview'
import { fakePlugins } from '@/test/plugins'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers, sidebar, sidebarLinks } from '@/test/shell'
import { routes } from './routes'

// 英文界面：这个文件的地址带 ?lang=en-US，整份按英文文案表渲染。中文界面的行为由其余测试覆盖，这里只确认各区域换成了英文。
describe('English console', () => {
  it('picks en-US from ?lang=', () => {
    expect(locale).toBe('en-US')
  })

  it('sidebar and scope switcher', async () => {
    const agent = makeAgent({ name: 'dev-helper' })
    server.use(...baseHandlers([agent]))
    renderRoutes(routes, defaultAgentPath(agent.id, 'config'))
    const list = await within(sidebar()).findByRole('list', { name: 'Agents' })
    await within(list).findByRole('link', { name: 'dev-helper' })
    expect(sidebarLinks().map((link) => link.textContent)).toEqual([
      'LOOP NOW',
      'Overview',
      'Tasks',
      'Project folder',
      'Project settings',
      'dev-helper',
      'Plugin market',
      'Skills library',
      'Stats',
      'External identities',
      'Global settings',
    ])
    expect(within(sidebar()).getByRole('button', { name: 'Switch scope, current: 测试项目' })).toBeInTheDocument()
  })

  it('overview sections and run status summary', async () => {
    const { snapshot } = await renderOverview()
    await snapshot([makeAgentOverview({ name: 'dev-helper' })])
    for (const name of ['Run status', 'Usage', 'Issues and failures']) expect(screen.getByRole('region', { name })).toBeInTheDocument()
    const tiles = screen.getByRole('list', { name: 'Run status summary' })
    for (const label of ['Replying', 'Idle', 'Session lost', 'Queued']) expect(within(tiles).getByText(label)).toBeInTheDocument()
  })

  it('chat composer', async () => {
    await renderChat()
    const box = screen.getByRole('textbox', { name: 'Message' })
    expect(box).toHaveAttribute('placeholder', 'Type a message. Enter to send, Shift+Enter for a new line')
    expect(screen.getByRole('button', { name: 'Send' })).toBeInTheDocument()
  })

  it('Agent config sections', async () => {
    await renderConfig()
    for (const name of ['Basics', 'Model account credential', 'Skills', 'Available CLIs', 'Delete Agent']) {
      expect(screen.getByRole('region', { name })).toBeInTheDocument()
    }
  })

  it('plugin market', async () => {
    server.use(...fakePlugins({ details: [] }).handlers, ...baseHandlers([]))
    renderRoutes(routes, '/plugins')
    expect(await screen.findByRole('heading', { name: 'Plugin market', level: 1 })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Install plugin' })).toBeInTheDocument()
  })

  it('global settings shows English as the chosen language', async () => {
    server.use(...baseHandlers([]))
    renderRoutes(routes, '/settings')
    const select = await screen.findByRole('combobox', { name: 'Language' })
    expect(select).toHaveDisplayValue('English')
    expect(select).toHaveAccessibleDescription('Applies to this browser only. The page reloads when you switch.')
  })

  it("known error codes drop the server's Chinese detail; unknown ones show it as is", () => {
    expect(describeError(new ApiError({ status: 409, code: 'agent_busy', serverMessage: 'Agent 正忙，请稍后再试' }))).toBe('The Agent is busy')
    expect(describeError(new ApiError({ status: 502, code: 'unknown', serverMessage: '上游错误' }))).toBe('上游错误')
  })
})
