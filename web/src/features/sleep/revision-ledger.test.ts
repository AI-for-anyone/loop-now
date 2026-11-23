import { expect, it } from 'vitest'
import { RevisionLedger } from './revision-ledger'

it('a new sleep is not cleared by an old null, an old terminal state or a repeat of the same revision; a higher revision can end it', () => {
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

it('an old empty task HTTP list cannot clear a new sleep known from events; once a new snapshot clears it, old events do not bring it back', () => {
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

it('normal sleep, two tasks with the same config and different runtimes keep separate revisions', () => {
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
