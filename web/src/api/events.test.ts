import { describe, expect, it } from 'vitest'
import { compareEventIds } from './events'

describe('compareEventIds', () => {
  it('按 Redis Stream 条目 ID 的时间与序号比较', () => {
    expect(compareEventIds('1700000000000-1', '1700000000000-0')).toBe(1)
    expect(compareEventIds('1700000000000-0', '1700000000001-0')).toBe(-1)
    expect(compareEventIds('1700000000000-2', '1700000000000-2')).toBe(0)
    expect(compareEventIds('99-0', '100-0')).toBe(-1)
  })
})
