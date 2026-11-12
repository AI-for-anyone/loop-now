import { describe, expect, it } from 'vitest'
import { sleepWakeNote } from './wake-text'

const id = '00000000-0000-7000-8000-000000005201'

describe('sleep 唤醒消息里取说明', () => {
  it('英文句式（现在）：取 "; note: " 之后的说明，多行合成一行；没有说明时为空', () => {
    expect(sleepWakeNote(`Platform notice: your sleep is due; continue in the original conversation. sleepId=${id}; note: check CI\n  then report`)).toEqual({ note: 'check CI then report' })
    expect(sleepWakeNote(`Platform notice: your sleep is due; continue in the original conversation. sleepId=${id}`)).toEqual({ note: '' })
  })

  it('中文句式（改成英文之前的旧会话）同样认', () => {
    expect(sleepWakeNote(`平台通知：sleep已到期，请接着原会话处理。sleepId=${id}；说明：等评审机器人`)).toEqual({ note: '等评审机器人' })
    expect(sleepWakeNote(`平台通知：sleep已到期，请接着原会话处理。sleepId=${id}`)).toEqual({ note: '' })
  })

  it('两种句式不混用，对不上时为 null', () => {
    expect(sleepWakeNote(`Platform notice: your sleep is due; continue in the original conversation. sleepId=${id}；说明：混用`)).toBeNull()
    expect(sleepWakeNote(`Platform notice: your sleep is due. sleepId=${id}`)).toBeNull()
    expect(sleepWakeNote('随便一条消息')).toBeNull()
  })
})
