import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'
import { commandList, platformCommands } from '@/test/commands'

// Whether a command can be used right now depends on the session state: as soon as the
// session changes, the list must be read again instead of waiting for the cache to expire.
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
    // The cached list is invalidated and read again the next time the menu opens (no need to wait for the 30-second cache to expire).
    await user.type(input, '/comp')
    await waitFor(() => expect(screen.getByRole('option', { name: /\/compact/ })).toBeInTheDocument())
    expect(reads).toBe(2)
    expect(screen.queryByRole('group', { name: '现在不能用' })).toBeNull()
  })

  it('只在 session.updated 时重读：平台命令执行完、没有会话变化时不额外读', async () => {
    let reads = 0
    const { agent, emit, user } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/commands'), () => { reads += 1; return HttpResponse.json(commandList()) }),
        http.post(apiUrl('/agents/:agentId/messages'), () => HttpResponse.json({ handled: true, source: 'platform', plugin: '', reply: null, command: 'reload', processStopped: true })),
      ],
    })
    await emit('snapshot', { status: makeStatus(agent.id), currentText: null, queuedRuns: [], queueItems: [] })
    const input = screen.getByLabelText('消息')
    await user.type(input, '/reload')
    await waitFor(() => expect(reads).toBe(1))
    await user.keyboard('{Escape}{Enter}')
    await screen.findByText('已停止旧进程，下一条消息将重新加载')
    await user.type(input, '/rel')
    await waitFor(() => expect(screen.getByRole('option', { name: /\/reload/ })).toBeInTheDocument())
    expect(reads).toBe(1)
  })
})
