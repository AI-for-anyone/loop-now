import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'
import { commandList, platformCommands } from '@/test/commands'

// 命令此刻能不能用取决于会话状态：会话一变就要重新读，不能等缓存过期。
describe('命令列表跟着会话状态更新', () => {
  it('收到 session.updated 后重新读命令列表：刚建好会话，/compact 不再是“还没有会话”', async () => {
    let session: 'none' | 'active' = 'none'
    let reads = 0
    const { agent, emit, user } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/commands'), () => {
          reads += 1
          return HttpResponse.json(commandList(platformCommands(session === 'none' ? { compact: { available: false, unavailableReason: 'session_none' } } : {})))
        }),
      ],
    })
    await emit('snapshot', { status: makeStatus(agent.id), currentText: null, queuedRuns: [], queueItems: [] })
    const input = screen.getByLabelText('消息')
    await user.type(input, '/comp')
    expect(await screen.findByRole('group', { name: '现在不能用' })).toHaveTextContent('还没有会话')
    await user.keyboard('{Escape}')
    await userEvent.clear(input)
    session = 'active'
    await emit('session.updated', { state: 'active', epoch: 1 })
    // 缓存的列表作废，下次打开菜单时重新读（不必等 30 秒的缓存过期）。
    await user.type(input, '/comp')
    await waitFor(() => expect(screen.getByRole('option', { name: /\/compact/ })).toBeInTheDocument())
    expect(reads).toBe(2)
    expect(screen.queryByRole('group', { name: '现在不能用' })).toBeNull()
  })
})
