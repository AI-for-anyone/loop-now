import { focusManager } from '@tanstack/react-query'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { apiUrl, context, errorBody } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

afterEach(() => vi.restoreAllMocks())

const deviceOne: Schemas['Device'] = {
  id: '01900000-0000-7000-8000-000000000001',
  name: 'iPhone · Safari',
  createdAt: '2026-11-28T08:00:00Z',
  lastSeenAt: '2026-11-28T09:15:00Z',
  lastAddress: '127.0.0.1',
  expiresAt: '2026-12-28T09:15:00Z',
}

const signedOutAccount: Schemas['RelayAccount'] = {
  relayUrl: 'https://relay.loop-now.app',
  signedIn: false,
  accountId: null,
  signedInAt: null,
  signIn: null,
  error: null,
}

const signedInAccount: Schemas['RelayAccount'] = {
  relayUrl: 'https://relay.loop-now.app',
  signedIn: true,
  accountId: 'user_2abc123',
  signedInAt: '2026-11-28T10:00:00Z',
  signIn: null,
  error: null,
}

const offTunnel: Schemas['Tunnel'] = {
  enabled: false,
  state: 'off',
  hostname: null,
  url: null,
  error: null,
  errorCode: null,
  retryAt: null,
  download: null,
  cloudflared: null,
}

describe('Phones and other devices settings', () => {
  it('signs in to a Loop Now account in the browser, supports popup fallback and cancel, and signs out', async () => {
    let account: Schemas['RelayAccount'] = { ...signedOutAccount }
    let tunnel: Schemas['Tunnel'] = { ...offTunnel }
    let signInCalls = 0
    let cancelCalls = 0
    let signOutCalls = 0
    const signInUrl = 'https://relay.loop-now.app/connect?state=s1&code_challenge=c1'

    server.use(
      http.get(apiUrl('/relay-account'), () => HttpResponse.json(account)),
      http.post(apiUrl('/relay-account/sign-in'), () => {
        signInCalls += 1
        const signIn: Schemas['RelaySignIn'] = {
          url: signInUrl,
          expiresAt: new Date(Date.now() + 10 * 60_000).toISOString(),
        }
        account = { ...account, signIn, error: null }
        return HttpResponse.json(signIn, { status: 201 })
      }),
      http.delete(apiUrl('/relay-account/sign-in'), () => {
        cancelCalls += 1
        account = { ...account, signIn: null }
        return new HttpResponse(null, { status: 204 })
      }),
      http.delete(apiUrl('/relay-account'), () => {
        signOutCalls += 1
        account = { ...signedOutAccount }
        tunnel = { ...tunnel, enabled: false, state: 'off' }
        return new HttpResponse(null, { status: 204 })
      }),
      http.get(apiUrl('/tunnel'), () => HttpResponse.json(tunnel)),
      ...baseHandlers([]),
    )

    const user = userEvent.setup()
    // First attempt: browser blocks the popup so the manual link notice is shown.
    const openSpy = vi.spyOn(window, 'open').mockReturnValue(null)
    renderRoutes(routes, '/settings/devices')

    const accountSection = await screen.findByRole('region', { name: 'Loop Now account' })
    const tunnelSection = await screen.findByRole('region', { name: 'Access from phones' })
    expect(await within(tunnelSection).findByRole('switch', { name: 'Allow access from phones' })).toBeDisabled()

    await user.click(await within(accountSection).findByRole('button', { name: 'Sign in to Loop Now' }))
    expect(signInCalls).toBe(1)
    expect(await within(accountSection).findByText('Waiting for sign-in in the browser…')).toBeInTheDocument()
    expect(within(accountSection).getByText(/browser blocked the sign-in window/i)).toBeInTheDocument()
    expect(within(accountSection).getByRole('link', { name: 'Open sign-in page' })).toHaveAttribute('href', signInUrl)
    expect(within(accountSection).getByText(signInUrl)).toBeInTheDocument()

    await user.click(within(accountSection).getByRole('button', { name: 'Cancel sign-in' }))
    await waitFor(() => expect(cancelCalls).toBe(1))
    expect(await within(accountSection).findByRole('button', { name: 'Sign in to Loop Now' })).toBeInTheDocument()

    // Second attempt: popup opens and navigates to the relay's /connect URL, then sign-in completes on refresh.
    const child = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    openSpy.mockReturnValue(child as unknown as Window)
    await user.click(within(accountSection).getByRole('button', { name: 'Sign in to Loop Now' }))
    await waitFor(() => expect(child.location.replace).toHaveBeenCalledWith(signInUrl))
    expect(within(accountSection).queryByText(/browser blocked the sign-in window/i)).toBeNull()

    account = { ...signedInAccount }
    focusManager.setFocused(false)
    focusManager.setFocused(true)
    try {
      expect(await within(accountSection).findByText('Signed in')).toBeInTheDocument()
      expect(within(accountSection).getByText('user_2abc123')).toBeInTheDocument()
      expect(within(tunnelSection).getByRole('switch', { name: 'Allow access from phones' })).toBeEnabled()
    } finally {
      focusManager.setFocused(undefined)
    }

    await user.click(within(accountSection).getByRole('button', { name: 'Sign out' }))
    await waitFor(() => expect(signOutCalls).toBe(1))
    expect(await within(accountSection).findByRole('button', { name: 'Sign in to Loop Now' })).toBeInTheDocument()
    expect(within(tunnelSection).getByRole('switch', { name: 'Allow access from phones' })).toBeDisabled()
  })

  it('shows last sign-in failure when the browser sign-in did not finish', async () => {
    server.use(
      http.get(apiUrl('/relay-account'), () =>
        HttpResponse.json({ ...signedOutAccount, error: 'The sign-in was cancelled in the browser.' }),
      ),
      ...baseHandlers([]),
    )

    renderRoutes(routes, '/settings/devices')
    const accountSection = await screen.findByRole('region', { name: 'Loop Now account' })
    expect(await within(accountSection).findByText('Last sign-in did not finish')).toBeInTheDocument()
    expect(within(accountSection).getByText('The sign-in was cancelled in the browser.')).toBeInTheDocument()
  })

  it('turns Allow access from phones on through downloading/online, uses the public address in QR pairing, turns off, and releases the address', async () => {
    let remote: Schemas['RemoteAccess'] = { enabled: false, port: null, state: 'off', url: null, error: null }
    let tunnel: Schemas['Tunnel'] = { ...offTunnel }
    let releaseAttempts = 0

    server.use(
      http.get(apiUrl('/relay-account'), () => HttpResponse.json(signedInAccount)),
      http.get(apiUrl('/remote-access'), () => HttpResponse.json(remote)),
      http.get(apiUrl('/tunnel'), () => HttpResponse.json(tunnel)),
      http.put(apiUrl('/tunnel'), async ({ request }) => {
        const body = (await request.json()) as Schemas['TunnelSettings']
        if (body.enabled) {
          remote = { enabled: true, port: null, state: 'listening', url: 'http://127.0.0.1:53124', error: null }
          tunnel = {
            ...tunnel,
            enabled: true,
            state: 'downloading',
            hostname: 'k3v9q2x7m4tb.loop-now.app',
            url: 'https://k3v9q2x7m4tb.loop-now.app',
            download: { version: '2026.11.2', receivedBytes: 10 * 1024 * 1024, totalBytes: 20 * 1024 * 1024 },
          }
        } else {
          tunnel = { ...tunnel, enabled: false, state: 'off', download: null }
        }
        return HttpResponse.json(tunnel)
      }),
      http.delete(apiUrl('/tunnel/address'), () => {
        releaseAttempts += 1
        if (releaseAttempts === 1) {
          return HttpResponse.json(errorBody('relay_failed', 'Could not reach the relay'), { status: 502 })
        }
        tunnel = { ...offTunnel }
        return new HttpResponse(null, { status: 204 })
      }),
      http.post(apiUrl('/device-pairings'), () => {
        const id = '01900000-0000-7000-8000-000000000201'
        return HttpResponse.json(
          {
            id,
            code: 'pub-secret',
            url: 'https://k3v9q2x7m4tb.loop-now.app/pair#pub-secret',
            expiresAt: new Date(Date.now() + 10 * 60_000).toISOString(),
          },
          { status: 201 },
        )
      }),
      ...baseHandlers([]),
    )

    const user = userEvent.setup()
    const writeText = vi.spyOn(navigator.clipboard, 'writeText')
    renderRoutes(routes, '/settings/devices')

    const tunnelSection = await screen.findByRole('region', { name: 'Access from phones' })
    const toggle = await within(tunnelSection).findByRole('switch', { name: 'Allow access from phones' })
    await waitFor(() => expect(toggle).toBeEnabled())
    expect(toggle).toHaveAttribute('aria-checked', 'false')

    await user.click(toggle)
    expect(await within(tunnelSection).findByText('Downloading cloudflared…')).toBeInTheDocument()
    const bar = within(tunnelSection).getByRole('progressbar', { name: 'cloudflared download progress' })
    expect(bar).toHaveAttribute('aria-valuenow', '50')
    expect(within(tunnelSection).getByText(/10\.0 MiB \/ 20\.0 MiB \(50%\)/)).toBeInTheDocument()

    // Transition to online with cloudflared metadata.
    tunnel = {
      ...tunnel,
      state: 'online',
      download: null,
      cloudflared: { path: '/data/tools/cloudflared/2026.11.2/darwin-arm64/cloudflared', version: '2026.11.2', source: 'downloaded' },
    }
    focusManager.setFocused(false)
    focusManager.setFocused(true)
    try {
      expect(await within(tunnelSection).findByText('Online')).toBeInTheDocument()
      expect(within(tunnelSection).getByText('https://k3v9q2x7m4tb.loop-now.app')).toBeInTheDocument()
      expect(within(tunnelSection).getByText('cloudflared 2026.11.2 (downloaded)')).toBeInTheDocument()
    } finally {
      focusManager.setFocused(undefined)
    }

    await user.click(within(tunnelSection).getByRole('button', { name: 'Copy' }))
    expect(writeText).toHaveBeenCalledWith('https://k3v9q2x7m4tb.loop-now.app')

    // Opening Add device while online uses the public address and does not show the local-address warning.
    const addButton = screen.getByRole('button', { name: 'Add device' })
    await waitFor(() => expect(addButton).toBeEnabled())
    await user.click(addButton)
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByText('https://k3v9q2x7m4tb.loop-now.app/pair#pub-secret')).toBeInTheDocument()
    expect(within(dialog).queryByText(/local address/)).toBeNull()
    await user.click(within(dialog).getAllByRole('button', { name: 'Close' }).at(-1)!)

    // Turning the switch off stops the tunnel while keeping the reserved address visible.
    await user.click(toggle)
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'false'))
    expect(within(tunnelSection).getByText('Reserved address')).toBeInTheDocument()
    expect(within(tunnelSection).getByText('https://k3v9q2x7m4tb.loop-now.app')).toBeInTheDocument()

    // Releasing the address asks for confirmation, shows relay errors inside the dialog, and clears the address on success.
    await user.click(within(tunnelSection).getByRole('button', { name: 'Release address' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(within(confirm).getByText(/k3v9q2x7m4tb\.loop-now\.app/)).toBeInTheDocument()
    await user.click(within(confirm).getByRole('button', { name: 'Release address' }))
    expect(await within(confirm).findByText(/Could not reach the relay/)).toBeInTheDocument()

    await user.click(within(confirm).getByRole('button', { name: 'Release address' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull())
    await waitFor(() => expect(within(tunnelSection).queryByText('https://k3v9q2x7m4tb.loop-now.app')).toBeNull())
  })

  it('shows clear explanations for tunnel failure codes, automatic retry countdown, and signed-out recovery', async () => {
    let tunnel: Schemas['Tunnel'] = {
      enabled: true,
      state: 'failed',
      hostname: 'k3v9q2x7m4tb.loop-now.app',
      url: 'https://k3v9q2x7m4tb.loop-now.app',
      error: 'cloudflared exited with status 1',
      errorCode: 'cloudflared_failed',
      retryAt: new Date(Date.now() + 25_000).toISOString(),
      download: null,
      cloudflared: null,
    }
    let account: Schemas['RelayAccount'] = { ...signedInAccount }
    server.use(
      http.get(apiUrl('/relay-account'), () => HttpResponse.json(account)),
      http.get(apiUrl('/tunnel'), () => HttpResponse.json(tunnel)),
      ...baseHandlers([]),
    )

    renderRoutes(routes, '/settings/devices')
    const tunnelSection = await screen.findByRole('region', { name: 'Access from phones' })
    expect(await within(tunnelSection).findByText('Failed')).toBeInTheDocument()
    expect(within(tunnelSection).getByText(/cloudflared exited or could not connect/)).toBeInTheDocument()
    expect(within(tunnelSection).getByText('cloudflared exited with status 1')).toBeInTheDocument()
    expect(within(tunnelSection).getByText(/Retrying automatically in/)).toBeInTheDocument()

    // When the relay rejects the credential (401 -> signed_out), the switch stays on and can still be turned off while Release requires sign-in.
    account = { ...signedOutAccount }
    tunnel = {
      ...tunnel,
      error: 'The Loop Now account signed this computer out.',
      errorCode: 'signed_out',
      retryAt: null,
    }
    focusManager.setFocused(false)
    focusManager.setFocused(true)
    try {
      expect(await within(tunnelSection).findByText(/signed out by the relay/i)).toBeInTheDocument()
      expect(within(tunnelSection).getByRole('switch', { name: 'Allow access from phones' })).toBeEnabled()
      expect(within(tunnelSection).getByRole('button', { name: 'Release address' })).toBeDisabled()
      expect(within(tunnelSection).getByText('Sign in above to release this address.')).toBeInTheDocument()
    } finally {
      focusManager.setFocused(undefined)
    }
  })

  it('toggles the local remote door, validates and saves a custom port, and shows failed listener errors', async () => {
    let state: Schemas['RemoteAccess'] = { enabled: false, port: null, state: 'off', url: null, error: null }
    const puts: Schemas['RemoteAccessSettings'][] = []
    server.use(
      http.get(apiUrl('/remote-access'), () => HttpResponse.json(state)),
      http.put(apiUrl('/remote-access'), async ({ request }) => {
        const body = (await request.json()) as Schemas['RemoteAccessSettings']
        puts.push(body)
        if (body.port === 9999) {
          state = { enabled: body.enabled, port: 9999, state: 'failed', url: null, error: 'listen tcp 127.0.0.1:9999: address already in use' }
        } else if (body.enabled) {
          const port = body.port ?? 53124
          state = { enabled: true, port: body.port ?? null, state: 'listening', url: `http://127.0.0.1:${port}`, error: null }
        } else {
          state = { enabled: false, port: body.port ?? null, state: 'off', url: null, error: null }
        }
        return HttpResponse.json(state)
      }),
      ...baseHandlers([]),
    )

    const user = userEvent.setup()
    renderRoutes(routes, '/settings/devices')

    const remoteSection = await screen.findByRole('region', { name: 'Local remote door' })
    const toggle = await within(remoteSection).findByRole('switch', { name: 'Enable local remote door' })
    expect(toggle).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByRole('button', { name: 'Add device' })).toBeDisabled()

    await user.click(toggle)
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'true'))
    expect(within(remoteSection).getByText('Listening')).toBeInTheDocument()
    expect(within(remoteSection).getByText('http://127.0.0.1:53124')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add device' })).toBeEnabled()
    expect(puts[0]).toEqual({ enabled: true, port: null })

    const portInput = within(remoteSection).getByLabelText('Port')
    await user.type(portInput, '70000')
    await user.click(within(remoteSection).getByRole('button', { name: 'Save port' }))
    expect(within(remoteSection).getByRole('alert')).toHaveTextContent('Enter a whole number between 1 and 65535')
    expect(puts).toHaveLength(1)

    await user.clear(portInput)
    await user.type(portInput, '9999')
    await user.click(within(remoteSection).getByRole('button', { name: 'Save port' }))
    expect(await within(remoteSection).findByText('Could not start')).toBeInTheDocument()
    expect(within(remoteSection).getByText(/address already in use/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Add device' })).toBeDisabled()
  })

  it('opens Add device dialog with QR code and fragment pairing link, ignores unrelated devices, names the device that used this code and hides the used QR', async () => {
    let seq = 0
    const devices: Schemas['Device'][] = []
    const statuses = new Map<string, Schemas['DevicePairingStatus']>()
    const unrelatedDevice: Schemas['Device'] = {
      ...deviceOne,
      id: '01900000-0000-7000-8000-000000000099',
      name: 'Other Tablet · Chrome',
    }
    server.use(
      http.get(apiUrl('/remote-access'), () =>
        HttpResponse.json({ enabled: true, port: null, state: 'listening', url: 'http://127.0.0.1:53124', error: null }),
      ),
      http.get(apiUrl('/devices'), () => HttpResponse.json({ devices })),
      http.post(apiUrl('/device-pairings'), () => {
        seq += 1
        const id = `01900000-0000-7000-8000-00000000010${seq}`
        const code = `code-${seq}-secret`
        const expiresAt = new Date(Date.now() + 10 * 60_000).toISOString()
        statuses.set(id, { id, state: 'open', expiresAt, device: null })
        return HttpResponse.json(
          {
            id,
            code,
            url: `http://127.0.0.1:53124/pair#${code}`,
            expiresAt,
          },
          { status: 201 },
        )
      }),
      http.get(apiUrl('/device-pairings/:pairingId'), ({ params }) => {
        const status = statuses.get(String(params.pairingId))
        return status ? HttpResponse.json(status) : new HttpResponse(null, { status: 404 })
      }),
      ...baseHandlers([]),
    )

    const user = userEvent.setup()
    const writeText = vi.spyOn(navigator.clipboard, 'writeText')
    renderRoutes(routes, '/settings/devices')

    const addButton = await screen.findByRole('button', { name: 'Add device' })
    await waitFor(() => expect(addButton).toBeEnabled())
    await user.click(addButton)

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('img', { name: 'Device pairing QR code' })).toBeInTheDocument()
    expect(within(dialog).getByText('http://127.0.0.1:53124/pair#code-1-secret')).toBeInTheDocument()
    expect(within(dialog).getByText(/local address/)).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: 'Copy link' }))
    expect(writeText).toHaveBeenCalledWith('http://127.0.0.1:53124/pair#code-1-secret')
    expect(await within(dialog).findByRole('button', { name: 'Copied' })).toBeInTheDocument()

    await user.click(within(dialog).getByRole('button', { name: 'New code' }))
    expect(await within(dialog).findByText('http://127.0.0.1:53124/pair#code-2-secret')).toBeInTheDocument()

    // An unrelated device pairing through another code must not trigger this dialog's paired notice.
    devices.push(unrelatedDevice)
    focusManager.setFocused(false)
    focusManager.setFocused(true)
    try {
      await waitFor(() => expect(screen.getByText('Other Tablet · Chrome')).toBeInTheDocument())
      expect(within(dialog).queryByText(/Paired:/)).toBeNull()
      expect(within(dialog).getByRole('img', { name: 'Device pairing QR code' })).toBeInTheDocument()

      // Now simulate the phone redeeming THIS code (code 2).
      const id2 = '01900000-0000-7000-8000-000000000102'
      devices.push(deviceOne)
      statuses.set(id2, { ...statuses.get(id2)!, state: 'used', device: deviceOne })
      focusManager.setFocused(false)
      focusManager.setFocused(true)
      expect(await within(dialog).findByText('Paired: iPhone · Safari')).toBeInTheDocument()
      // The used QR code and one-time link must disappear immediately.
      expect(within(dialog).queryByRole('img', { name: 'Device pairing QR code' })).toBeNull()
      expect(within(dialog).queryByText('http://127.0.0.1:53124/pair#code-2-secret')).toBeNull()
    } finally {
      focusManager.setFocused(undefined)
    }

    // Generating a new code after one was used brings back a fresh QR code.
    await user.click(within(dialog).getByRole('button', { name: 'New code' }))
    expect(await within(dialog).findByText('http://127.0.0.1:53124/pair#code-3-secret')).toBeInTheDocument()
    expect(within(dialog).getByRole('img', { name: 'Device pairing QR code' })).toBeInTheDocument()
    expect(within(dialog).queryByText('Paired: iPhone · Safari')).toBeNull()

    await user.click(within(dialog).getAllByRole('button', { name: 'Close' }).at(-1)!)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('shows the expired badge when a pairing code has already expired', async () => {
    const expiredId = '01900000-0000-7000-8000-000000000199'
    const expiresAt = new Date(Date.now() - 1000).toISOString()
    server.use(
      http.get(apiUrl('/remote-access'), () =>
        HttpResponse.json({ enabled: true, port: null, state: 'listening', url: 'http://127.0.0.1:53124', error: null }),
      ),
      http.post(apiUrl('/device-pairings'), () =>
        HttpResponse.json(
          {
            id: expiredId,
            code: 'expired-secret',
            url: 'http://127.0.0.1:53124/pair#expired-secret',
            expiresAt,
          },
          { status: 201 },
        ),
      ),
      http.get(apiUrl(`/device-pairings/${expiredId}`), () =>
        HttpResponse.json({ id: expiredId, state: 'expired', expiresAt, device: null }),
      ),
      ...baseHandlers([]),
    )

    const user = userEvent.setup()
    renderRoutes(routes, '/settings/devices')
    const addButton = await screen.findByRole('button', { name: 'Add device' })
    await waitFor(() => expect(addButton).toBeEnabled())
    await user.click(addButton)

    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText('Pairing code expired')).toBeInTheDocument()
  })

  it('lists paired devices with metadata, renames a device inline, and removes a device after confirmation', async () => {
    let devices: Schemas['Device'][] = [deviceOne]
    server.use(
      http.get(apiUrl('/remote-access'), () =>
        HttpResponse.json({ enabled: true, port: null, state: 'listening', url: 'http://127.0.0.1:53124', error: null }),
      ),
      http.get(apiUrl('/devices'), () => HttpResponse.json({ devices })),
      http.patch(apiUrl(`/devices/${deviceOne.id}`), async ({ request }) => {
        const body = (await request.json()) as Schemas['DevicePatch']
        const updated = { ...deviceOne, name: body.name }
        devices = [updated]
        return HttpResponse.json(updated)
      }),
      http.delete(apiUrl(`/devices/${deviceOne.id}`), () => {
        devices = []
        return new HttpResponse(null, { status: 204 })
      }),
      ...baseHandlers([]),
    )

    const user = userEvent.setup()
    renderRoutes(routes, '/settings/devices')

    const listSection = await screen.findByRole('region', { name: 'Paired devices' })
    expect(await within(listSection).findByText('iPhone · Safari')).toBeInTheDocument()
    expect(within(listSection).getByText('127.0.0.1')).toBeInTheDocument()

    await user.click(within(listSection).getByRole('button', { name: 'Rename iPhone · Safari' }))
    const input = within(listSection).getByLabelText('Device name')
    await user.clear(input)
    await user.type(input, 'Work iPhone')
    await user.click(within(listSection).getByRole('button', { name: 'Save' }))
    expect(await within(listSection).findByText('Work iPhone')).toBeInTheDocument()

    await user.click(within(listSection).getByRole('button', { name: 'Remove Work iPhone' }))
    const confirm = await screen.findByRole('alertdialog')
    expect(within(confirm).getByText('Remove "Work iPhone"?')).toBeInTheDocument()
    await user.click(within(confirm).getByRole('button', { name: 'Remove' }))

    expect(await within(listSection).findByText('No paired devices yet.')).toBeInTheDocument()
  })

  it('on the remote door, shows a computer-only notice and never calls local-only account, tunnel or device endpoints', async () => {
    let localCalls = 0
    const forbid = () => {
      localCalls += 1
      return HttpResponse.json({ error: { code: 'forbidden', message: 'Local only' } }, { status: 403 })
    }
    server.use(
      http.get(apiUrl('/context'), () => HttpResponse.json({ ...context, door: 'remote' })),
      http.get(apiUrl('/relay-account'), forbid),
      http.get(apiUrl('/tunnel'), forbid),
      http.get(apiUrl('/remote-access'), forbid),
      http.get(apiUrl('/devices'), forbid),
      ...baseHandlers([]),
    )

    renderRoutes(routes, '/settings/devices')

    expect(
      await screen.findByText(/only be managed on the computer running Loop Now/),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add device' })).toBeNull()
    expect(screen.queryByRole('button', { name: 'Sign in to Loop Now' })).toBeNull()
    expect(localCalls).toBe(0)
  })
})
