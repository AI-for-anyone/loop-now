import { act, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { extractPairingCode } from '@/api/devices'
import { routes } from '@/app/routes'
import { t } from '@/i18n'
import { apiUrl, context, errorBody } from '@/test/api'
import { FakeEventSource } from '@/test/fake-event-source'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const pairedDevice: Schemas['Device'] = {
  id: '01900000-0000-7000-8000-000000000001',
  name: 'iPhone · Safari',
  createdAt: '2026-11-28T08:00:00Z',
  lastSeenAt: '2026-11-28T08:00:00Z',
  lastAddress: '127.0.0.1',
  expiresAt: '2026-12-28T08:00:00Z',
}

describe('extractPairingCode', () => {
  it('accepts full pairing URLs, fragments, or bare codes', () => {
    expect(extractPairingCode('http://127.0.0.1:53124/pair#abc-123')).toBe('abc-123')
    expect(extractPairingCode('https://host.loop-now.app/pair#xyz_789')).toBe('xyz_789')
    expect(extractPairingCode('#only-hash')).toBe('only-hash')
    expect(extractPairingCode('  bare-code  ')).toBe('bare-code')
    expect(extractPairingCode('   ')).toBe('')
  })
})

describe('PairPage (/pair)', () => {
  it('auto-redeems a fragment code without calling authenticated endpoints first, strips the URL fragment, and enters the console', async () => {
    const received: Schemas['DeviceSessionRequest'][] = []
    let contextCalls = 0
    server.use(
      http.post(apiUrl('/device-sessions'), async ({ request }) => {
        received.push((await request.json()) as Schemas['DeviceSessionRequest'])
        return HttpResponse.json(pairedDevice, { status: 201 })
      }),
      http.get(apiUrl('/context'), () => {
        contextCalls += 1
        return HttpResponse.json({ ...context, door: 'remote' })
      }),
      ...baseHandlers([]),
    )

    window.history.replaceState(null, '', '/pair#one-time-secret')
    const replaceSpy = vi.spyOn(window.history, 'replaceState')
    try {
      const { router } = renderRoutes(routes, '/pair#one-time-secret')

      await waitFor(() => expect(router.state.location.pathname).toBe('/'))
      expect(received).toEqual([{ code: 'one-time-secret' }])
      expect(replaceSpy).toHaveBeenCalledWith(null, '', '/pair')
      // The console asks for /context once it has mounted, which can come after the navigation itself.
      await waitFor(() => expect(contextCalls).toBeGreaterThan(0))
    } finally {
      window.history.replaceState(null, '', '/')
    }
  })

  it('shows clear errors for invalid/expired codes, rate limits, and local-door attempts, and allows manual retry', async () => {
    let attempt = 0
    const received: Schemas['DeviceSessionRequest'][] = []
    server.use(
      http.post(apiUrl('/device-sessions'), async ({ request }) => {
        const body = (await request.json()) as Schemas['DeviceSessionRequest']
        received.push(body)
        attempt += 1
        if (attempt === 1) return HttpResponse.json(errorBody('pairing_code_invalid'), { status: 403 })
        if (attempt === 2) return HttpResponse.json(errorBody('rate_limited'), { status: 429 })
        return HttpResponse.json(pairedDevice, { status: 201 })
      }),
      ...baseHandlers([]),
    )

    const user = userEvent.setup()
    const { router } = renderRoutes(routes, '/pair#used-code')

    expect(await screen.findByText(/invalid, expired, or has already been used/i)).toBeInTheDocument()

    const codeInput = screen.getByLabelText('Pairing link or code')
    const nameInput = screen.getByLabelText('Device name (optional)')
    await user.type(codeInput, 'http://127.0.0.1:53124/pair#second-code')
    await user.type(nameInput, 'Pocket Phone')
    await user.click(screen.getByRole('button', { name: 'Pair device' }))

    expect(await screen.findByText(/Too many failed attempts/i)).toBeInTheDocument()

    await user.clear(codeInput)
    await user.type(codeInput, 'fresh-code')
    await user.click(screen.getByRole('button', { name: 'Pair device' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(received).toEqual([
      { code: 'used-code' },
      { code: 'second-code', name: 'Pocket Phone' },
      { code: 'fresh-code', name: 'Pocket Phone' },
    ])
  })

  it('offers a direct link to the console when a pairing link is opened on the local door', async () => {
    server.use(
      http.post(apiUrl('/device-sessions'), () =>
        HttpResponse.json(errorBody('forbidden', 'Devices pair at the remote door; this computer needs no pairing.'), { status: 403 }),
      ),
      ...baseHandlers([]),
    )

    renderRoutes(routes, '/pair#local-code')

    expect(await screen.findByText(/This computer doesn't need pairing/i)).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open console' })).toHaveAttribute('href', '/')
  })

  it('redeems a new fragment code when the hash changes via hashchange while /pair is already mounted', async () => {
    const received: Schemas['DeviceSessionRequest'][] = []
    server.use(
      http.post(apiUrl('/device-sessions'), async ({ request }) => {
        const body = (await request.json()) as Schemas['DeviceSessionRequest']
        received.push(body)
        if (body.code === 'expired-code') {
          return HttpResponse.json(errorBody('pairing_code_invalid'), { status: 403 })
        }
        return HttpResponse.json(pairedDevice, { status: 201 })
      }),
      http.get(apiUrl('/context'), () => HttpResponse.json({ ...context, door: 'remote' })),
      ...baseHandlers([]),
    )

    window.history.replaceState(null, '', '/pair#expired-code')
    const replaceSpy = vi.spyOn(window.history, 'replaceState')
    try {
      const { router } = renderRoutes(routes, '/pair#expired-code')

      expect(await screen.findByText(/invalid, expired, or has already been used/i)).toBeInTheDocument()
      expect(window.location.hash).toBe('')

      // Simulate pasting a new pairing link in the same tab (same-document fragment navigation).
      replaceSpy.mockClear()
      window.history.pushState(null, '', '/pair#fresh-same-tab-code')
      act(() => {
        window.dispatchEvent(new HashChangeEvent('hashchange'))
      })

      await waitFor(() => expect(router.state.location.pathname).toBe('/'))
      expect(window.location.hash).toBe('')
      expect(replaceSpy).toHaveBeenCalledWith(null, '', '/pair')
      expect(received).toEqual([{ code: 'expired-code' }, { code: 'fresh-same-tab-code' }])
    } finally {
      window.history.replaceState(null, '', '/')
    }
  })

  it('redeems a new fragment code when React Router updates location.hash while /pair is mounted', async () => {
    const received: Schemas['DeviceSessionRequest'][] = []
    server.use(
      http.post(apiUrl('/device-sessions'), async ({ request }) => {
        const body = (await request.json()) as Schemas['DeviceSessionRequest']
        received.push(body)
        if (body.code === 'old-code') {
          return HttpResponse.json(errorBody('pairing_code_invalid'), { status: 403 })
        }
        return HttpResponse.json(pairedDevice, { status: 201 })
      }),
      http.get(apiUrl('/context'), () => HttpResponse.json({ ...context, door: 'remote' })),
      ...baseHandlers([]),
    )

    const { router } = renderRoutes(routes, '/pair#old-code')
    expect(await screen.findByText(/invalid, expired, or has already been used/i)).toBeInTheDocument()

    await act(async () => {
      await router.navigate('/pair#router-new-code')
    })

    await waitFor(() => expect(router.state.location.pathname).toBe('/'))
    expect(received).toEqual([{ code: 'old-code' }, { code: 'router-new-code' }])
  })
})

describe('Remote door authentication gate and immediate revocation', () => {
  it('shows the pairing gate when an unpaired device opens the remote door, and enters the console after pairing in place', async () => {
    let paired = false
    server.use(
      http.get(apiUrl('/context'), () =>
        paired
          ? HttpResponse.json({ ...context, door: 'remote' })
          : HttpResponse.json(errorBody('unauthenticated'), { status: 401 }),
      ),
      http.get(apiUrl('/health'), () =>
        paired
          ? HttpResponse.json({ ok: true, database: { ok: true, error: null, walBytes: { data: 0, events: 0 }, walWarning: false } })
          : HttpResponse.json(errorBody('unauthenticated'), { status: 401 }),
      ),
      http.get(apiUrl('/projects'), () =>
        paired
          ? HttpResponse.json({ projects: [] })
          : HttpResponse.json(errorBody('unauthenticated'), { status: 401 }),
      ),
      http.post(apiUrl('/device-sessions'), () => {
        paired = true
        return HttpResponse.json(pairedDevice, { status: 201 })
      }),
      ...baseHandlers([]),
    )

    const user = userEvent.setup()
    renderRoutes(routes, '/')

    expect(await screen.findByRole('heading', { level: 1, name: 'Pair this device' })).toBeInTheDocument()

    await user.type(screen.getByLabelText('Pairing link or code'), 'https://demo.loop-now.app/pair#valid-code')
    await user.click(screen.getByRole('button', { name: 'Pair device' }))

    await waitFor(() => expect(screen.queryByRole('heading', { level: 1, name: 'Pair this device' })).toBeNull())
    expect(await screen.findByRole('navigation', { name: t.shell.nav })).toBeInTheDocument()
  })

  it('loses access immediately when the server closes the live event stream after device removal', async () => {
    let revoked = false
    server.use(
      http.get(apiUrl('/context'), () =>
        revoked
          ? HttpResponse.json(errorBody('unauthenticated'), { status: 401 })
          : HttpResponse.json({ ...context, door: 'remote' }),
      ),
      ...baseHandlers([]),
    )

    renderRoutes(routes, '/')
    expect(await screen.findByRole('navigation', { name: t.shell.nav })).toBeInTheDocument()

    const inboxStream = FakeEventSource.instances.find((es) => es.url.includes('/inbox/events'))
    expect(inboxStream).toBeDefined()
    inboxStream!.open()

    // Removing the device on the computer cancels the open SSE stream, making the browser drop to reconnecting.
    revoked = true
    inboxStream!.interrupt()

    expect(await screen.findByRole('heading', { level: 1, name: 'Pair this device' })).toBeInTheDocument()
    expect(screen.queryByRole('navigation', { name: t.shell.nav })).toBeNull()
  })
})
