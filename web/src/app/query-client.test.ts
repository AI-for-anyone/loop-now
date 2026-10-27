import { MutationObserver, onlineManager } from '@tanstack/react-query'
import { waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createQueryClient } from './query-client'

describe('查询客户端', () => {
  afterEach(() => onlineManager.setOnline(true))

  it('浏览器判断离线时，查询与修改仍照常发往本机服务端', async () => {
    onlineManager.setOnline(false)
    const client = createQueryClient()
    const queryFn = vi.fn(async () => 1)
    const mutationFn = vi.fn(async () => 2)
    await expect(client.fetchQuery({ queryKey: ['offline'], queryFn })).resolves.toBe(1)
    void new MutationObserver(client, { mutationFn }).mutate()
    await waitFor(() => expect(mutationFn).toHaveBeenCalled())
  })
})
