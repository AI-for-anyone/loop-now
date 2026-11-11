import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { t } from '@/i18n'
import { apiUrl, errorBody } from '@/test/api'
import { renderWithQuery } from '@/test/render'
import { runtimeList } from '@/test/runtimes'
import { server } from '@/test/server'
import { RuntimesCard } from './RuntimesCard'

// 全局设置里的 Agent 运行时（GET /runtimes）。
function setup(list: Schemas['RuntimeList'] | (() => Response)) {
  const queries: string[] = []
  server.use(
    http.get(apiUrl('/runtimes'), ({ request }) => {
      queries.push(new URL(request.url).search)
      return typeof list === 'function' ? list() : HttpResponse.json(list)
    }),
  )
  renderWithQuery(<RuntimesCard />)
  return { user: userEvent.setup(), queries }
}
const claude = () => screen.findByRole('region', { name: 'Claude Code' })
const notFound = { available: false, version: null, binaryPath: null, unavailableReason: 'not_found', login: null } as const

describe('全局设置的 Agent 运行时', () => {
  it('Pi 写版本；Claude Code 写版本、路径、登录方式与订阅、检查时间，可用且已登录时没有提醒', async () => {
    setup(runtimeList())
    const cc = await claude()
    const pi = screen.getByRole('region', { name: 'Pi' })
    expect(pi).toHaveTextContent('Pi版本 1.1.0可用')
    expect(cc).toHaveTextContent('Claude Code版本 2.1.0可用已登录')
    expect(cc).toHaveTextContent('路径/usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js')
    expect(cc).toHaveTextContent('登录claude.ai · 订阅 max')
    expect(cc).toHaveTextContent(`检查于 ${t.chat.time('2026-11-11T08:00:00Z')}`)
    expect(within(cc).queryByRole('note')).toBeNull()
  })

  it('没找到 claude：标不可用，说明装好并确认终端里能运行', async () => {
    setup(runtimeList(notFound))
    const cc = await claude()
    expect(cc).toHaveTextContent(/^Claude Code不可用/)
    expect(within(cc).getByRole('note')).toHaveTextContent(/^本机没找到 claude：装好 Claude Code，并确认在终端里能运行 claude。$/)
    expect(cc).not.toHaveTextContent('路径')
  })

  it('读不出版本：说明确认在终端里能正常运行 claude', async () => {
    setup(runtimeList({ available: false, version: null, unavailableReason: 'version_failed', login: null }))
    expect(within(await claude()).getByRole('note')).toHaveTextContent('运行 claude --version 失败：确认在终端里能正常运行 claude。')
  })

  it('没登录：标未登录，说明在本机终端运行 claude 并登录', async () => {
    setup(runtimeList({ login: { state: 'logged_out', authMethod: null, subscriptionType: null } }))
    const cc = await claude()
    expect(cc).toHaveTextContent('可用未登录')
    expect(within(cc).getByRole('note')).toHaveTextContent('本机的 Claude Code 没有登录：在本机终端运行 claude 并登录。')
    expect(cc).not.toHaveTextContent('登录claude')
  })

  it('登录状态判断不了：不当作出错，只说明运行失败时会如实报错', async () => {
    setup(runtimeList({ login: { state: 'unknown', authMethod: null, subscriptionType: null } }))
    const cc = await claude()
    expect(cc).toHaveTextContent('登录状态未知')
    expect(within(cc).queryByRole('note')).toBeNull()
    expect(cc).toHaveTextContent('判断不了登录状态；Agent 运行失败时会如实报错。')
  })

  it('“重新检查”不用服务端缓存（refresh=true），换上新结果', async () => {
    let current = runtimeList(notFound)
    const { user, queries } = setup(() => HttpResponse.json(current))
    expect(within(await claude()).getByRole('note')).toBeInTheDocument()
    current = runtimeList()
    await user.click(screen.getByRole('button', { name: '重新检查' }))
    await waitFor(() => expect(within(screen.getByRole('region', { name: 'Claude Code' })).queryByRole('note')).toBeNull())
    expect(queries).toEqual(['', '?refresh=true'])
    expect(screen.getByRole('region', { name: 'Claude Code' })).toHaveTextContent('已登录')
  })

  it('读不到时显示原因', async () => {
    setup(() => HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 }))
    expect(await screen.findByRole('alert')).toHaveTextContent('服务端内部错误')
  })
})
