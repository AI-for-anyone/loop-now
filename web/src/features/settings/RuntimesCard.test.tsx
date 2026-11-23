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

// Agent runtimes in global settings (GET /runtimes).
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

describe('Agent runtimes in global settings', () => {
  it('Pi shows its version; Claude Code shows version, path, login method and subscription, and check time, with no warning when available and logged in', async () => {
    setup(runtimeList())
    const cc = await claude()
    const pi = screen.getByRole('region', { name: 'Pi' })
    expect(pi).toHaveTextContent('PiVersion 1.1.0Available')
    expect(cc).toHaveTextContent('Claude CodeVersion 2.1.0AvailableLogged in')
    expect(cc).toHaveTextContent('Path/usr/local/lib/node_modules/@anthropic-ai/claude-code/cli.js')
    expect(cc).toHaveTextContent('Loginclaude.ai · max subscription')
    expect(cc).toHaveTextContent(`Checked ${t.chat.time('2026-11-11T08:00:00Z')}`)
    expect(within(cc).queryByRole('note')).toBeNull()
  })

  it('claude not found: marked unavailable, with a note to install it and make sure it runs in a terminal', async () => {
    setup(runtimeList(notFound))
    const cc = await claude()
    expect(cc).toHaveTextContent(/^Claude CodeUnavailable/)
    expect(within(cc).getByRole('note')).toHaveTextContent(/^claude wasn't found on this computer: install Claude Code and make sure claude runs in a terminal\.$/)
    expect(cc).not.toHaveTextContent('Path')
  })

  it('version cannot be read: a note to make sure claude runs normally in a terminal', async () => {
    setup(runtimeList({ available: false, version: null, unavailableReason: 'version_failed', login: null }))
    expect(within(await claude()).getByRole('note')).toHaveTextContent('Running claude --version failed: make sure claude runs normally in a terminal.')
  })

  it('not logged in: marked logged out, with a note to run claude in a terminal on this computer and log in', async () => {
    setup(runtimeList({ login: { state: 'logged_out', authMethod: null, subscriptionType: null } }))
    const cc = await claude()
    expect(cc).toHaveTextContent('AvailableLogged out')
    expect(within(cc).getByRole('note')).toHaveTextContent('Claude Code isn\'t logged in on this computer: run claude in a terminal there and log in.')
    expect(cc).not.toHaveTextContent('Loginclaude')
  })

  it('login status cannot be determined: not treated as an error, only a note that a failed run reports the real error', async () => {
    setup(runtimeList({ login: { state: 'unknown', authMethod: null, subscriptionType: null } }))
    const cc = await claude()
    expect(cc).toHaveTextContent('Login status unknown')
    expect(within(cc).queryByRole('note')).toBeNull()
    expect(cc).toHaveTextContent('The login status can\'t be determined; if a run fails, the error says why.')
  })

  it('“Check again” bypasses the server cache (refresh=true) and shows the new result', async () => {
    let current = runtimeList(notFound)
    const { user, queries } = setup(() => HttpResponse.json(current))
    expect(within(await claude()).getByRole('note')).toBeInTheDocument()
    current = runtimeList()
    await user.click(screen.getByRole('button', { name: 'Check again' }))
    await waitFor(() => expect(within(screen.getByRole('region', { name: 'Claude Code' })).queryByRole('note')).toBeNull())
    expect(queries).toEqual(['', '?refresh=true'])
    expect(screen.getByRole('region', { name: 'Claude Code' })).toHaveTextContent('Logged in')
  })

  it('shows the reason when the runtimes cannot be read', async () => {
    setup(() => HttpResponse.json(errorBody('internal_error', 'boom'), { status: 500 }))
    expect(await screen.findByRole('alert')).toHaveTextContent('Internal server error')
  })
})
