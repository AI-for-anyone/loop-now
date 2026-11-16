import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { apiUrl, makeStatus } from '@/test/api'
import { renderChat } from '@/test/chat'
import { commandList, platformCommands } from '@/test/commands'

// Whether a command can be used right now depends on the session state: as soon as the
// session changes, the list must be read again instead of waiting for the cache to expire.
describe('The command list follows the session state', () => {
  it('reads the command list again after session.updated: once the session is created, /compact is no longer “No session yet”', async () => {
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
    const input = screen.getByLabelText('Message')
    await user.type(input, '/comp')
    expect(await screen.findByRole('group', { name: 'Not available now' })).toHaveTextContent('No session yet')
    await user.keyboard('{Escape}')
    await userEvent.clear(input)
    session = 'active'
    await emit('session.updated', { state: 'active', epoch: 1 })
    // The cached list is invalidated and read again the next time the menu opens (no need to wait for the 30-second cache to expire).
    await user.type(input, '/comp')
    await waitFor(() => expect(screen.getByRole('option', { name: /\/compact/ })).toBeInTheDocument())
    expect(reads).toBe(2)
    expect(screen.queryByRole('group', { name: 'Not available now' })).toBeNull()
  })

  it('reads again only on session.updated: no extra read after a platform command finishes without a session change', async () => {
    let reads = 0
    const { agent, emit, user } = await renderChat({
      handlers: [
        http.get(apiUrl('/agents/:agentId/commands'), () => { reads += 1; return HttpResponse.json(commandList()) }),
        http.post(apiUrl('/agents/:agentId/messages'), () => HttpResponse.json({ handled: true, source: 'platform', plugin: '', reply: null, command: 'reload', processStopped: true })),
      ],
    })
    await emit('snapshot', { status: makeStatus(agent.id), currentText: null, queuedRuns: [], queueItems: [] })
    const input = screen.getByLabelText('Message')
    await user.type(input, '/reload')
    await waitFor(() => expect(reads).toBe(1))
    await user.keyboard('{Escape}{Enter}')
    await screen.findByText('Old process stopped; the next message will reload it')
    await user.type(input, '/rel')
    await waitFor(() => expect(screen.getByRole('option', { name: /\/reload/ })).toBeInTheDocument())
    expect(reads).toBe(1)
  })
})
