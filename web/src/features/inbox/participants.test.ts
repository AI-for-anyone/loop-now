import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { partyDisplay, partyName } from './participants'

const party = (overrides: Partial<Schemas['InboxParticipant']>): Schemas['InboxParticipant'] => ({ kind: 'agent', id: 'a1', address: 'demo/writer', name: 'writer', deleted: false, pluginId: null, channelId: null, ...overrides })

// The server's names for the console, the platform and deleted parties are fixed text in the server's language; the console names
// them itself from kind and deleted, and uses the server's name only when nothing else tells who it is.
describe('partyDisplay', () => {
  it('names the console and the platform in the UI language, whatever the server sends', () => {
    expect(partyDisplay(party({ kind: 'console', id: null, address: '@console', name: 'Console' }))).toEqual({ title: '控制台', deleted: null, address: '@console' })
    expect(partyDisplay(party({ kind: 'platform', id: null, address: null, name: 'Platform' }))).toEqual({ title: '平台', deleted: null, address: null })
  })

  it('marks deleted Agents and identities in the UI language; the title comes from the original address', () => {
    expect(partyDisplay(party({ deleted: true, name: 'Deleted Agent', address: 'demo/writer' }))).toEqual({ title: 'writer', deleted: '已删除 Agent', address: 'demo/writer' })
    expect(partyDisplay(party({ kind: 'external', deleted: true, name: 'Deleted identity', address: '@design-review' }))).toEqual({ title: 'design-review', deleted: '已删除身份', address: '@design-review' })
  })

  it('a deleted party without an address is titled by the deleted mark', () => {
    expect(partyDisplay(party({ kind: 'external', deleted: true, name: 'Deleted identity', address: null }))).toEqual({ title: '已删除身份', deleted: null, address: null })
  })

  it('live Agents and identities keep their own names', () => {
    expect(partyDisplay(party({}))).toEqual({ title: 'writer', deleted: null, address: 'demo/writer' })
    expect(partyName(party({ kind: 'external', name: '设计评审组', address: '@design-review' }))).toBe('设计评审组')
    expect(partyName(party({ kind: 'console', name: 'Console' }))).toBe('控制台')
  })
})
