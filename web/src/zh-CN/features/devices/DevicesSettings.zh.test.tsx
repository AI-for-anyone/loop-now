import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { apiUrl } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

describe('Phones and other devices settings in Chinese UI', () => {
  it('shows the Chinese tab label, Add device button, QR dialog and Remove action', async () => {
    server.use(
      http.get(apiUrl('/remote-access'), () =>
        HttpResponse.json({ enabled: true, port: null, state: 'listening', url: 'http://127.0.0.1:53124', error: null }),
      ),
      http.get(apiUrl('/devices'), () =>
        HttpResponse.json({
          devices: [
            {
              id: '01900000-0000-7000-8000-000000000001',
              name: 'iPhone · Safari',
              createdAt: '2026-11-28T08:00:00Z',
              lastSeenAt: '2026-11-28T09:15:00Z',
              lastAddress: '127.0.0.1',
              expiresAt: '2026-12-28T09:15:00Z',
            },
          ],
        }),
      ),
      http.post(apiUrl('/device-pairings'), () =>
        HttpResponse.json(
          {
            code: 'zh-secret',
            url: 'http://127.0.0.1:53124/pair#zh-secret',
            expiresAt: new Date(Date.now() + 10 * 60_000).toISOString(),
          },
          { status: 201 },
        ),
      ),
      ...baseHandlers([]),
    )

    const user = userEvent.setup()
    renderRoutes(routes, '/settings/devices')

    const nav = await screen.findByRole('navigation', { name: '全局设置分组' })
    expect(within(nav).getByRole('link', { name: '手机和其它设备' })).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByText('运行中')).toBeInTheDocument()
    expect(await screen.findByText('iPhone · Safari')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '移除 iPhone · Safari' })).toBeInTheDocument()

    const add = screen.getByRole('button', { name: '添加设备' })
    await waitFor(() => expect(add).toBeEnabled())
    await user.click(add)

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('img', { name: '设备配对二维码' })).toBeInTheDocument()
    expect(within(dialog).getByText('http://127.0.0.1:53124/pair#zh-secret')).toBeInTheDocument()
  })
})
