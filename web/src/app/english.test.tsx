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

// English UI: this file's URL carries ?lang=en-US, so the whole file renders with the English string table. The
// other tests cover the behavior of the Chinese UI; here we only confirm that each area has switched to English.
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
    expect(box).toHaveAttribute('placeholder', 'Message 开发助手')
    // The English UI writes Enter in the key hints.
    expect(screen.getByText('Shift + Enter')).toBeInTheDocument()
    expect(screen.getByText('new line')).toBeInTheDocument()
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
    // The language picker is a segmented control since the redesign (it was a select).
    const group = await screen.findByRole('radiogroup', { name: 'Language' })
    expect(within(group).getByRole('radio', { name: 'English' })).toHaveAttribute('aria-checked', 'true')
    expect(group).toHaveAccessibleDescription('Applies to this browser only. The page reloads when you switch.')
  })

  it("known error codes add the server's detail unless it only repeats the code; unknown ones show it as is", () => {
    expect(describeError(new ApiError({ status: 409, code: 'agent_busy', serverMessage: 'run 7f3a is still replying' }))).toBe('The Agent is busy: run 7f3a is still replying')
    expect(describeError(new ApiError({ status: 409, code: 'agent_busy', serverMessage: 'Agent busy.' }))).toBe('The Agent is busy')
    expect(describeError(new ApiError({ status: 409, code: 'agent_busy', serverMessage: 'Agent 正忙' }))).toBe('The Agent is busy')
    expect(describeError(new ApiError({ status: 502, code: 'unknown', serverMessage: 'upstream gateway failed' }))).toBe('upstream gateway failed')
  })
})
