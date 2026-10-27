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

// 顶栏的连接状态区域；“部分依赖不可用”在全局设置页上也会出现，断言都限定在这个区域内。
const topStatus = () => screen.getByRole('status', { name: '连接状态' })
const banner = () => screen.queryByText(/控制台访问的服务端地址是/)

const degraded = () => HttpResponse.json({ ok: false, postgres: { ok: true }, redis: { ok: false, error: '连接被拒绝' } }, { status: 503 })

describe('顶栏连接状态', () => {
  it('首次健康查询还没有结果时显示检查中，并显示服务端的主机和端口', async () => {
    server.use(
      http.get(apiUrl('/health'), async () => {
        await delay('infinite')
        return HttpResponse.json({})
      }),
      ...baseHandlers([]),
    )
    renderRoutes(routes, defaultAgentsPath)
    expect(within(topStatus()).getByText('检查中…')).toBeInTheDocument()
    expect(within(topStatus()).getByText('127.0.0.1:8780')).toBeInTheDocument()
  })

  it('健康检查报告全部依赖正常时显示已连接', async () => {
    server.use(...baseHandlers([]))
    renderRoutes(routes, defaultAgentsPath)
    expect(await within(topStatus()).findByText('已连接')).toBeInTheDocument()
  })

  it('健康检查报告有依赖不可用时显示部分依赖不可用，不显示无法连接的全局提示', async () => {
    server.use(http.get(apiUrl('/health'), degraded), ...baseHandlers([]))
    renderRoutes(routes, defaultAgentsPath)
    expect(await within(topStatus()).findByText('部分依赖不可用')).toBeInTheDocument()
    expect(banner()).toBeNull()
  })

  it('请求没有到达服务端时显示无法连接，全局提示照常显示', async () => {
    server.use(http.all(`${config.apiBase}/*`, () => HttpResponse.error()))
    renderRoutes(routes, defaultAgentsPath)
    expect(await within(topStatus()).findByText('无法连接')).toBeInTheDocument()
    await waitFor(() => expect(banner()).not.toBeNull())
  })

  it('健康接口返回 503 以外的错误时显示无法连接', async () => {
    server.use(http.get(apiUrl('/health'), () => HttpResponse.json(errorBody('not_found'), { status: 404 })), ...baseHandlers([]))
    renderRoutes(routes, defaultAgentsPath)
    expect(await within(topStatus()).findByText('无法连接')).toBeInTheDocument()
    // 请求到达了服务端，所以不显示无法连接的全局提示。
    expect(banner()).toBeNull()
  })

  it('服务端停止期间显示无法连接，恢复后下一次健康查询时显示已连接', async () => {
    let up = false
    let healthCalls = 0
    // 在健康查询开始轮询前接管计时器，定时刷新才能被推进。
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      server.use(
        http.all(`${config.apiBase}/*`, () => (up ? undefined : HttpResponse.error())),
        http.get(apiUrl('/health'), () => {
          healthCalls += 1
          return HttpResponse.json({ ok: true, postgres: { ok: true }, redis: { ok: true } })
        }),
        ...baseHandlers([]),
      )
      renderRoutes(routes, defaultAgentsPath)
      expect(await within(topStatus()).findByText('无法连接')).toBeInTheDocument()

      up = true
      const before = healthCalls
      await act(async () => {
        await vi.advanceTimersByTimeAsync(config.agentListPollMs + 100)
      })
      await waitFor(() => expect(within(topStatus()).getByText('已连接')).toBeInTheDocument())
      expect(healthCalls).toBeGreaterThan(before)
      await waitFor(() => expect(banner()).toBeNull())
    } finally {
      vi.useRealTimers()
    }
  })

  it('窗口重新获得焦点时也查询健康状态', async () => {
    let healthy = true
    server.use(
      http.get(apiUrl('/health'), () => (healthy ? HttpResponse.json({ ok: true, postgres: { ok: true }, redis: { ok: true } }) : degraded())),
      ...baseHandlers([]),
    )
    renderRoutes(routes, defaultAgentsPath)
    expect(await within(topStatus()).findByText('已连接')).toBeInTheDocument()
    healthy = false
    focusManager.setFocused(false)
    focusManager.setFocused(true)
    await waitFor(() => expect(within(topStatus()).getByText('部分依赖不可用')).toBeInTheDocument())
    focusManager.setFocused(undefined)
  })
})
