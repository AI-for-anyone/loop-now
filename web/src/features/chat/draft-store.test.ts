import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ImageDraft } from '@/features/images/image-drafts'
import {
  CHAT_DRAFTS_STORAGE_KEY,
  clearChatDraft,
  getChatDraft,
  getChatScrollTop,
  resetChatDraftsForTest,
  setChatDraftImages,
  setChatDraftText,
  setChatScrollTop,
} from './draft-store'

const sampleImage = (id: string): ImageDraft => ({
  id,
  file: new File(['png'], `${id}.png`, { type: 'image/png' }),
})

afterEach(() => {
  vi.restoreAllMocks()
  resetChatDraftsForTest()
})

describe('per-Agent chat draft store', () => {
  it('stores text per agentId in memory and sessionStorage under loop-now.chat-drafts', () => {
    setChatDraftText('agent-a', 'Draft for A')
    setChatDraftText('agent-b', 'Draft for B')

    expect(getChatDraft('agent-a').text).toBe('Draft for A')
    expect(getChatDraft('agent-b').text).toBe('Draft for B')
    expect(JSON.parse(window.sessionStorage.getItem(CHAT_DRAFTS_STORAGE_KEY) ?? '{}')).toEqual({
      'agent-a': 'Draft for A',
      'agent-b': 'Draft for B',
    })
  })

  it('preserves pending images and scrollTop in memory per agentId', () => {
    const img = sampleImage('shot-1')
    setChatDraftImages('agent-a', [img])
    setChatScrollTop('agent-a', 240)

    expect(getChatDraft('agent-a').images).toEqual([img])
    expect(getChatScrollTop('agent-a')).toBe(240)
    expect(getChatDraft('agent-b').images).toEqual([])
    expect(getChatScrollTop('agent-b')).toBeNull()
  })

  it('clears text and images on clearChatDraft while keeping scrollTop independent', () => {
    setChatDraftText('agent-a', 'Pending text')
    setChatDraftImages('agent-a', [sampleImage('shot-2')])
    setChatScrollTop('agent-a', 180)

    clearChatDraft('agent-a')

    expect(getChatDraft('agent-a').text).toBe('')
    expect(getChatDraft('agent-a').images).toEqual([])
    expect(getChatScrollTop('agent-a')).toBe(180)
    expect(window.sessionStorage.getItem(CHAT_DRAFTS_STORAGE_KEY)).toBeNull()
  })

  it('hydrates text from sessionStorage when memory is empty (e.g. after reload)', () => {
    window.sessionStorage.setItem(CHAT_DRAFTS_STORAGE_KEY, JSON.stringify({ 'agent-reload': 'Survives reload' }))
    expect(getChatDraft('agent-reload').text).toBe('Survives reload')
  })

  it('falls back to in-memory drafts when sessionStorage throws', () => {
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('QuotaExceededError')
    })
    setChatDraftText('agent-private', 'In-memory only')
    expect(getChatDraft('agent-private').text).toBe('In-memory only')
  })

  it('resets memory and sessionStorage via resetChatDraftsForTest', () => {
    setChatDraftText('agent-a', 'Text')
    setChatDraftImages('agent-a', [sampleImage('shot-3')])
    setChatScrollTop('agent-a', 90)

    resetChatDraftsForTest()

    expect(getChatDraft('agent-a')).toEqual({ text: '', images: [], scrollTop: null })
    expect(window.sessionStorage.getItem(CHAT_DRAFTS_STORAGE_KEY)).toBeNull()
  })
})
