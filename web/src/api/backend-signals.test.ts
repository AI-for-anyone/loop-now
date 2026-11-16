import { describe, expect, it } from 'vitest'
import { isMissingEndpoint } from './backend-signals'
import { ApiError } from './client'

const notFound = (serverMessage: string, details?: unknown) => new ApiError({ status: 404, code: 'not_found', serverMessage, details: details as never })

// An older server's fixed Chinese text “接口不存在” (no such endpoint) is recognized too: see src/zh-CN/api/backend-signals.zh.test.ts.
describe('isMissingEndpoint', () => {
  it('recognizes the machine-readable signal: details.reason unknown_endpoint on a 404 not_found', () => {
    expect(isMissingEndpoint(notFound('no such endpoint', { reason: 'unknown_endpoint' }))).toBe(true)
  })

  it('a missing resource is not a missing endpoint', () => {
    expect(isMissingEndpoint(notFound('task not found'))).toBe(false)
    expect(isMissingEndpoint(notFound('task not found', { reason: 'something_else' }))).toBe(false)
  })

  it('needs both 404 and not_found', () => {
    expect(isMissingEndpoint(new ApiError({ status: 409, code: 'task_conflict', serverMessage: '接口不存在', details: { reason: 'unknown_endpoint' } as never }))).toBe(false) // check-english: zh (an older server's fixed text)
    expect(isMissingEndpoint(new Error('接口不存在'))).toBe(false) // check-english: zh (an older server's fixed text)
  })
})
