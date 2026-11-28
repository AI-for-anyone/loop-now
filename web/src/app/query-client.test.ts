import { MutationObserver, onlineManager } from '@tanstack/react-query'
import { waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { createQueryClient } from './query-client'

describe('query client', () => {
  afterEach(() => onlineManager.setOnline(true))

  it('when the browser considers itself offline, queries and mutations still go to the local server as usual', async () => {
    onlineManager.setOnline(false)
    const client = createQueryClient()
    const queryFn = vi.fn(async () => 1)
    const mutationFn = vi.fn(async () => 2)
    await expect(client.fetchQuery({ queryKey: ['offline'], queryFn })).resolves.toBe(1)
    void new MutationObserver(client, { mutationFn }).mutate()
    await waitFor(() => expect(mutationFn).toHaveBeenCalled())
  })

  it('does not retry 401 unauthenticated responses', async () => {
    const client = createQueryClient()
    const queryFn = vi.fn(async () => {
      throw new ApiError({ status: 401, code: 'unauthenticated' })
    })
    await expect(client.fetchQuery({ queryKey: ['unauth'], queryFn })).rejects.toMatchObject({ status: 401 })
    expect(queryFn).toHaveBeenCalledTimes(1)
  })
})
