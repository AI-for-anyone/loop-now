import { expect, it } from 'vitest'
import { RevisionLedger } from './revision-ledger'

it('新sleep不被旧null、旧终态或重复同版本清除；更高版本可结束', () => {
  const ledger = new RevisionLedger<string | null>()
  ledger.observe('normal/A', 10, 'sleep-first')
  ledger.observe('normal/A', 11, null)
  ledger.observe('normal/A', 12, 'sleep-second')
  expect(ledger.observe('normal/A', 11, null)).toBe(false)
  expect(ledger.observe('normal/A', 10, 'sleep-first')).toBe(false)
  expect(ledger.observe('normal/A', 12, null)).toBe(false)
  expect(ledger.current('normal/A')?.value).toBe('sleep-second')
  expect(ledger.observe('normal/A', 13, null)).toBe(true)
  expect(ledger.current('normal/A')?.value).toBeNull()
})

it('task旧HTTP空列表不能清掉事件已知的新sleep，新快照清除后旧事件不复活', () => {
  const ledger = new RevisionLedger<string | null>()
  ledger.acceptOwner('task/T', 20)
  ledger.observe('task/T/runtime/R', 5, 'sleep-first')
  expect(ledger.clearFromSnapshot('task/T', 19, 'task/T/runtime/R', null)).toBe(false)
  expect(ledger.current('task/T/runtime/R')?.value).toBe('sleep-first')
  expect(ledger.clearFromSnapshot('task/T', 21, 'task/T/runtime/R', null)).toBe(true)
  expect(ledger.observe('task/T/runtime/R', 5, 'sleep-first')).toBe(false)
  expect(ledger.acceptOwner('task/T', 20)).toBe(false)
  expect(ledger.current('task/T/runtime/R')?.value).toBeNull()
  ledger.acceptOwner('task/T', 22)
  expect(ledger.observe('task/T/runtime/R', 7, 'sleep-second')).toBe(true)
})

it('normal、同配置的两任务及不同runtime保留独立版本', () => {
  const ledger = new RevisionLedger<string | null>()
  ledger.observe('normal/A', 30, 'normal')
  ledger.observe('task/T1/runtime/R1', 4, 'one')
  ledger.observe('task/T2/runtime/R2', 2, 'two')
  ledger.acceptOwner('task/T1', 9)
  ledger.clearFromSnapshot('task/T1', 10, 'task/T1/runtime/R1', null)
  expect(ledger.current('normal/A')?.value).toBe('normal')
  expect(ledger.current('task/T2/runtime/R2')?.value).toBe('two')
  expect(ledger.ownerVersion('task/T2')).toBe(-1)
})
