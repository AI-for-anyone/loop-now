import { focusManager } from '@tanstack/react-query'
import { act, screen, waitFor, within } from '@testing-library/react'
import { delay, http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import { config } from '@/config'
import { apiUrl, defaultAgentsPath, errorBody } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'
import { routes } from './routes'

// The connection status area in the top bar. “Database unavailable” also
// appears on the global settings page, so all assertions are scoped to this area.
const topStatus = () => screen.getByRole('status', { name: 'Connection' })
const banner = () => screen.queryByText(/The console talks to the server at/)

const degraded = () => HttpResponse.json({ ok: false, database: { ok: false, error: 'unable to open database file' } }, { status: 503 })

describe('top bar connection status', () => {
  it('shows Checking while the first health query has no result yet, and shows the host and port of the server', async () => {
    server.use(
      http.get(apiUrl('/health'), async () => {
        await delay('infinite')
        return HttpResponse.json({})
      }),
      ...baseHandlers([]),
    )
    renderRoutes(routes, defaultAgentsPath)
    expect(within(topStatus()).getByText('Checking…')).toBeInTheDocument()
    expect(within(topStatus()).getByText('127.0.0.1:8780')).toBeInTheDocument()
  })

  it('when the API base is a relative path (the server serves the console on the same port), shows the host and port of the current page', async () => {
    server.use(...baseHandlers([]))
    const original = config.apiBase
    config.apiBase = '/api/v1'
    try {
      renderRoutes(routes, defaultAgentsPath)
      expect(within(topStatus()).getByText(window.location.host)).toBeInTheDocument()
    } finally {
      config.apiBase = original
    }
  })

  it('shows Connected when the health check reports the database is fine', async () => {
    server.use(...baseHandlers([]))
    renderRoutes(routes, defaultAgentsPath)
    expect(await within(topStatus()).findByText('Connected')).toBeInTheDocument()
  })

  it('shows Database unavailable when the health check reports the database is unavailable, without the global unreachable notice', async () => {
    server.use(http.get(apiUrl('/health'), degraded), ...baseHandlers([]))
    renderRoutes(routes, defaultAgentsPath)
    expect(await within(topStatus()).findByText('Database unavailable')).toBeInTheDocument()
    expect(banner()).toBeNull()
  })

  it('an oversized database log is only a warning: still shows Connected', async () => {
    server.use(
      http.get(apiUrl('/health'), () => HttpResponse.json({ ok: true, database: { ok: true, error: null, walBytes: { data: 300 << 20, events: 0 }, walWarning: true } })),
      ...baseHandlers([]),
    )
    renderRoutes(routes, defaultAgentsPath)
    expect(await within(topStatus()).findByText('Connected')).toBeInTheDocument()
  })

  it('shows Unreachable when requests do not reach the server, and the global notice shows as usual', async () => {
    server.use(http.all(`${config.apiBase}/*`, () => HttpResponse.error()))
    renderRoutes(routes, defaultAgentsPath)
    expect(await within(topStatus()).findByText('Unreachable')).toBeInTheDocument()
    await waitFor(() => expect(banner()).not.toBeNull())
  })

  it('shows Unreachable when the health endpoint returns an error other than 503', async () => {
    server.use(http.get(apiUrl('/health'), () => HttpResponse.json(errorBody('not_found'), { status: 404 })), ...baseHandlers([]))
    renderRoutes(routes, defaultAgentsPath)
    expect(await within(topStatus()).findByText('Unreachable')).toBeInTheDocument()
    // The request reached the server, so the global notice about being unable to connect is not shown.
    expect(banner()).toBeNull()
  })

  it('shows Unreachable while the server is down, and Connected at the next health query after it is back', async () => {
    let up = false
    let healthCalls = 0
    // Take over the timers before the health query starts polling; only then can the periodic refresh be advanced.
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      server.use(
        http.all(`${config.apiBase}/*`, () => (up ? undefined : HttpResponse.error())),
        http.get(apiUrl('/health'), () => {
          healthCalls += 1
          return HttpResponse.json({ ok: true, database: { ok: true } })
        }),
        ...baseHandlers([]),
      )
      renderRoutes(routes, defaultAgentsPath)
      expect(await within(topStatus()).findByText('Unreachable')).toBeInTheDocument()

      up = true
      const before = healthCalls
      await act(async () => {
        await vi.advanceTimersByTimeAsync(config.agentListPollMs + 100)
      })
      await waitFor(() => expect(within(topStatus()).getByText('Connected')).toBeInTheDocument())
      expect(healthCalls).toBeGreaterThan(before)
      await waitFor(() => expect(banner()).toBeNull())
    } finally {
      vi.useRealTimers()
    }
  })

  it('also queries health when the window regains focus', async () => {
    let healthy = true
    server.use(
      http.get(apiUrl('/health'), () => (healthy ? HttpResponse.json({ ok: true, database: { ok: true } }) : degraded())),
      ...baseHandlers([]),
    )
    renderRoutes(routes, defaultAgentsPath)
    expect(await within(topStatus()).findByText('Connected')).toBeInTheDocument()
    healthy = false
    focusManager.setFocused(false)
    focusManager.setFocused(true)
    await waitFor(() => expect(within(topStatus()).getByText('Database unavailable')).toBeInTheDocument())
    focusManager.setFocused(undefined)
  })
})
