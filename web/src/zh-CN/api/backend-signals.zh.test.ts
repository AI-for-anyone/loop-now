import { describe, expect, it } from 'vitest'
import { isMissingEndpoint } from '@/api/backend-signals'
import { ApiError } from '@/api/client'

const notFound = (serverMessage: string, details?: unknown) => new ApiError({ status: 404, code: 'not_found', serverMessage, details: details as never })

// Servers from before the machine-readable signal said "no such endpoint" only in their fixed Chinese message.
describe('isMissingEndpoint', () => {
  it("still recognizes older servers' fixed message text", () => {
    expect(isMissingEndpoint(notFound('接口不存在'))).toBe(true)
  })
})
