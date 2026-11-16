import { describe, expect, it } from 'vitest'
import { sleepWakeNote } from './wake-text'

const id = '00000000-0000-7000-8000-000000005201'

// The Chinese form of sessions from before the switch to English: see src/zh-CN/features/sleep/wake-text.zh.test.ts.
describe('the note in a sleep wake-up message', () => {
  it('English form (current): takes the note after "; note: ", joining lines into one; empty without a note', () => {
    expect(sleepWakeNote(`Platform notice: your sleep is due; continue in the original conversation. sleepId=${id}; note: check CI\n  then report`)).toEqual({ note: 'check CI then report' })
    expect(sleepWakeNote(`Platform notice: your sleep is due; continue in the original conversation. sleepId=${id}`)).toEqual({ note: '' })
  })

  it('the two forms do not mix; null when nothing matches', () => {
    expect(sleepWakeNote(`Platform notice: your sleep is due; continue in the original conversation. sleepId=${id}；说明：混用`)).toBeNull() // check-english: zh (the Chinese form's separator, mixed into the English form)
    expect(sleepWakeNote(`Platform notice: your sleep is due. sleepId=${id}`)).toBeNull()
    expect(sleepWakeNote('just some message')).toBeNull()
  })
})
