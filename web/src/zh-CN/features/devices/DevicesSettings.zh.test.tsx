import { focusManager } from '@tanstack/react-query'
import { screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { apiUrl, context } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

describe('Phones and other devices settings in Chinese UI', () => {
  it('shows the Chinese tab label, Loop Now account, tunnel switch and online public QR pairing', async () => {
    server.use(
      http.get(apiUrl('/relay-account'), () =>
        HttpResponse.json({
          relayUrl: 'https://relay.loop-now.app',
          signedIn: true,
          accountId: 'user_zh_01',
          signedInAt: '2026-11-28T08:00:00Z',
          signIn: null,
          error: null,
        }),
      ),
      http.get(apiUrl('/tunnel'), () =>
        HttpResponse.json({
          enabled: true,
          state: 'online',
          hostname: 'k3v9q2x7m4tb.loop-now.app',
          url: 'https://k3v9q2x7m4tb.loop-now.app',
          error: null,
          errorCode: null,
          retryAt: null,
          download: null,
          cloudflared: { path: '/opt/homebrew/bin/cloudflared', version: '2026.11.2', source: 'path' },
        }),
      ),
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
            id: '01900000-0000-7000-8000-000000000101',
            code: 'zh-secret',
            url: 'https://k3v9q2x7m4tb.loop-now.app/pair#zh-secret',
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
    expect(await screen.findByText('已登录')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '退出登录' })).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: '允许从手机访问' })).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByText('已在线')).toBeInTheDocument()
    expect(screen.getByText('https://k3v9q2x7m4tb.loop-now.app')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '释放地址' })).toBeInTheDocument()
    expect(await screen.findByText('iPhone · Safari')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '移除 iPhone · Safari' })).toBeInTheDocument()

    const add = screen.getByRole('button', { name: '添加设备' })
    await waitFor(() => expect(add).toBeEnabled())
    await user.click(add)

    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('img', { name: '设备配对二维码' })).toBeInTheDocument()
    expect(within(dialog).getByText('https://k3v9q2x7m4tb.loop-now.app/pair#zh-secret')).toBeInTheDocument()
  })

  it('on the remote door in Chinese UI, the cannot-connect banner does not mention LOOP_NOW_CONSOLE_ORIGINS', async () => {
    let up = true
    server.use(
      http.get(apiUrl('/context'), () => (up ? HttpResponse.json({ ...context, door: 'remote' }) : HttpResponse.error())),
      http.get(apiUrl('/health'), () => (up ? HttpResponse.json({ ok: true, database: { ok: true } }) : HttpResponse.error())),
      ...baseHandlers([]),
    )

    renderRoutes(routes, '/settings/devices')
    expect(await screen.findByText(/只能在运行 Loop Now 的那台电脑上管理/)).toBeInTheDocument()

    up = false
    focusManager.setFocused(false)
    focusManager.setFocused(true)
    try {
      const alert = await screen.findByRole('alert')
      expect(alert).toHaveTextContent('无法连接到服务端')
      expect(alert).not.toHaveTextContent('LOOP_NOW_CONSOLE_ORIGINS')
    } finally {
      focusManager.setFocused(undefined)
    }
  })
})
