import { describe, expect, it } from 'vitest'
import { compareEventIds } from './events'

describe('compareEventIds', () => {
  it('compares event stream entry IDs by time, then sequence number', () => {
    expect(compareEventIds('1700000000000-1', '1700000000000-0')).toBe(1)
    expect(compareEventIds('1700000000000-0', '1700000000001-0')).toBe(-1)
    expect(compareEventIds('1700000000000-2', '1700000000000-2')).toBe(0)
    expect(compareEventIds('99-0', '100-0')).toBe(-1)
  })
})
