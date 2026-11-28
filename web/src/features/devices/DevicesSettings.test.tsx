import { focusManager } from '@tanstack/react-query'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it, vi } from 'vitest'
import type { Schemas } from '@/api/client'
import { routes } from '@/app/routes'
import { apiUrl, context } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

const deviceOne: Schemas['Device'] = {
  id: '01900000-0000-7000-8000-000000000001',
  name: 'iPhone · Safari',
  createdAt: '2026-11-28T08:00:00Z',
  lastSeenAt: '2026-11-28T09:15:00Z',
  lastAddress: '127.0.0.1',
  expiresAt: '2026-12-28T09:15:00Z',
}

describe('Phones and other devices settings', () => {
  it('toggles remote access, validates and saves a custom port, and shows failed listener errors', async () => {
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

    const remoteSection = await screen.findByRole('region', { name: 'Remote access' })
    const toggle = await within(remoteSection).findByRole('switch', { name: 'Allow access from phones and other devices' })
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

  it('opens Add device dialog with QR code and fragment pairing link, supports regenerate, and detects a newly paired device', async () => {
    let seq = 0
    const devices: Schemas['Device'][] = []
    server.use(
      http.get(apiUrl('/remote-access'), () =>
        HttpResponse.json({ enabled: true, port: null, state: 'listening', url: 'http://127.0.0.1:53124', error: null }),
      ),
      http.get(apiUrl('/devices'), () => HttpResponse.json({ devices })),
      http.post(apiUrl('/device-pairings'), () => {
        seq += 1
        const code = `code-${seq}-secret`
        return HttpResponse.json(
          {
            code,
            url: `http://127.0.0.1:53124/pair#${code}`,
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

    // Simulate the phone redeeming the code; the dialog's focus/poll refresh confirms the paired device.
    devices.push(deviceOne)
    focusManager.setFocused(false)
    focusManager.setFocused(true)
    try {
      expect(await within(dialog).findByText('Paired: iPhone · Safari')).toBeInTheDocument()
    } finally {
      focusManager.setFocused(undefined)
    }

    await user.click(within(dialog).getAllByRole('button', { name: 'Close' }).at(-1)!)
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('shows the expired badge when a pairing code has already expired', async () => {
    server.use(
      http.get(apiUrl('/remote-access'), () =>
        HttpResponse.json({ enabled: true, port: null, state: 'listening', url: 'http://127.0.0.1:53124', error: null }),
      ),
      http.post(apiUrl('/device-pairings'), () =>
        HttpResponse.json(
          {
            code: 'expired-secret',
            url: 'http://127.0.0.1:53124/pair#expired-secret',
            expiresAt: new Date(Date.now() - 1000).toISOString(),
          },
          { status: 201 },
        ),
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

  it('on the remote door, shows a computer-only notice and never calls local-only device endpoints', async () => {
    let localCalls = 0
    server.use(
      http.get(apiUrl('/context'), () => HttpResponse.json({ ...context, door: 'remote' })),
      http.get(apiUrl('/remote-access'), () => {
        localCalls += 1
        return HttpResponse.json({ error: { code: 'forbidden', message: 'Local only' } }, { status: 403 })
      }),
      http.get(apiUrl('/devices'), () => {
        localCalls += 1
        return HttpResponse.json({ error: { code: 'forbidden', message: 'Local only' } }, { status: 403 })
      }),
      ...baseHandlers([]),
    )

    renderRoutes(routes, '/settings/devices')

    expect(
      await screen.findByText(/only be managed on the computer running Loop Now/),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Add device' })).toBeNull()
    expect(localCalls).toBe(0)
  })
})
