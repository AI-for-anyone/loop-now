import { screen } from '@testing-library/react'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'
import { routes } from '@/app/routes'
import { apiUrl, errorBody } from '@/test/api'
import { renderRoutes } from '@/test/render'
import { server } from '@/test/server'
import { baseHandlers } from '@/test/shell'

describe('PairPage in Chinese UI', () => {
  it('shows the Chinese pairing gate when the remote door returns 401', async () => {
    server.use(
      http.get(apiUrl('/context'), () => HttpResponse.json(errorBody('unauthenticated'), { status: 401 })),
      http.get(apiUrl('/health'), () => HttpResponse.json(errorBody('unauthenticated'), { status: 401 })),
      ...baseHandlers([]),
    )

    renderRoutes(routes, '/')

    expect(await screen.findByRole('heading', { level: 1, name: '配对这台设备' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '配对并登录' })).toBeInTheDocument()
  })
})
