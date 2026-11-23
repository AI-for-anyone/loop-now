import { describe, expect, it } from 'vitest'
import { sleepWakeNote } from '@/features/sleep/wake-text'

const id = '00000000-0000-7000-8000-000000005201'

// Sessions from before the platform's wake-up message switched to English keep the Chinese form; it is not migrated.
describe('the note in a sleep wake-up message', () => {
  it('Chinese form (old sessions from before the switch to English) is recognized too', () => {
    expect(sleepWakeNote(`平台通知：sleep已到期，请接着原会话处理。sleepId=${id}；说明：等评审机器人`)).toEqual({ note: '等评审机器人' })
    expect(sleepWakeNote(`平台通知：sleep已到期，请接着原会话处理。sleepId=${id}`)).toEqual({ note: '' })
  })
})
