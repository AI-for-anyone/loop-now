import { describe, expect, it } from 'vitest'
import type { Schemas } from '@/api/client'
import { partyDisplay } from '@/features/inbox/participants'

const party = (overrides: Partial<Schemas['InboxParticipant']>): Schemas['InboxParticipant'] => ({ kind: 'agent', id: 'a1', address: 'demo/writer', name: 'writer', deleted: false, pluginId: null, channelId: null, ...overrides })

// The server names the console, the platform and deleted parties in English; only in the Chinese UI do these names differ from the
// console's own, so only here can the tests tell that the console names them itself. Moved from
// src/features/inbox/participants.test.ts, which keeps the same scenarios in English.
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
})
