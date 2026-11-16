import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/client'
import { describeError, serverDetail } from './errors'

const error = (serverMessage: string | null, code: ApiError['code'] = 'agent_busy') => new ApiError({ status: 409, code, serverMessage })

// The server's message is English detail: the English UI shows the code's copy followed by it (Chinese UI: see
// src/zh-CN/lib/errors.zh.test.ts).
describe('describeError (English UI)', () => {
  it('appends the server detail to the copy of a known code', () => {
    expect(describeError(error('run 7f3a is still replying'))).toBe('The Agent is busy: run 7f3a is still replying')
  })

  it.each([
    ['the code itself', 'agent_busy'],
    ['the code as words, any case, with a trailing period', 'Agent busy.'],
    ['the English copy of the code', 'The Agent is busy'],
    ['the Chinese copy of the code (older servers)', 'Agent 正忙'], // check-english: zh (older servers sent the Chinese copy)
    ['nothing', ''],
    ['no message at all', null],
  ])('drops a message that only repeats the code: %s', (_, message) => {
    expect(describeError(error(message))).toBe('The Agent is busy')
    expect(serverDetail(error(message))).toBeNull()
  })

  it('shows the server text as is for a code the console does not know', () => {
    expect(describeError(error('upstream gateway failed', 'unknown'))).toBe('upstream gateway failed')
  })
})
