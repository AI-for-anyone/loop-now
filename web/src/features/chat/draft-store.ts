import type { ImageDraft } from '@/features/images/image-drafts'

export const CHAT_DRAFTS_STORAGE_KEY = 'loop-now.chat-drafts'

export interface ChatDraft {
  text: string
  images: ImageDraft[]
  scrollTop: number | null
}

const drafts = new Map<string, ChatDraft>()
const persistedTextAgents = new Set<string>()

function readStoredTexts(): Record<string, string> | null {
  try {
    const raw = window.sessionStorage.getItem(CHAT_DRAFTS_STORAGE_KEY)
    if (!raw) return {}
    const parsed: unknown = JSON.parse(raw)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const result: Record<string, string> = {}
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value === 'string' && value !== '') result[key] = value
    }
    return result
  } catch {
    return null
  }
}

function writeStoredText(agentId: string, text: string): void {
  try {
    const current = readStoredTexts() ?? {}
    if (text === '') {
      delete current[agentId]
    } else {
      current[agentId] = text
    }
    if (Object.keys(current).length === 0) {
      window.sessionStorage.removeItem(CHAT_DRAFTS_STORAGE_KEY)
    } else {
      window.sessionStorage.setItem(CHAT_DRAFTS_STORAGE_KEY, JSON.stringify(current))
    }
    if (text === '') {
      persistedTextAgents.delete(agentId)
    } else {
      persistedTextAgents.add(agentId)
    }
  } catch {
    persistedTextAgents.delete(agentId)
    // Private browsing or quota limits: keep the draft in memory for this session.
  }
}

function pruneIfEmpty(agentId: string, entry: ChatDraft): void {
  if (entry.text === '' && entry.images.length === 0 && entry.scrollTop === null) {
    drafts.delete(agentId)
  }
}

function ensureEntry(agentId: string): ChatDraft {
  const existing = drafts.get(agentId)
  if (existing) return existing
  const stored = readStoredTexts()
  const entry: ChatDraft = {
    text: stored?.[agentId] ?? '',
    images: [],
    scrollTop: null,
  }
  drafts.set(agentId, entry)
  return entry
}

// Read the saved draft (text, pending images, and last non-bottom scroll offset) for an Agent.
export function getChatDraft(agentId: string): ChatDraft {
  const existing = drafts.get(agentId)
  const stored = readStoredTexts()
  if (existing) {
    if (persistedTextAgents.has(agentId) && stored !== null && !(agentId in stored)) {
      persistedTextAgents.delete(agentId)
      existing.text = ''
      pruneIfEmpty(agentId, existing)
    }
    return { text: existing.text, images: [...existing.images], scrollTop: existing.scrollTop }
  }
  return {
    text: stored?.[agentId] ?? '',
    images: [],
    scrollTop: null,
  }
}

export function setChatDraftText(agentId: string, text: string): void {
  const entry = ensureEntry(agentId)
  entry.text = text
  writeStoredText(agentId, text)
  pruneIfEmpty(agentId, entry)
}

export function setChatDraftImages(agentId: string, images: ImageDraft[]): void {
  const entry = ensureEntry(agentId)
  entry.images = [...images]
  pruneIfEmpty(agentId, entry)
}

export function getChatScrollTop(agentId: string): number | null {
  return drafts.get(agentId)?.scrollTop ?? null
}

export function setChatScrollTop(agentId: string, scrollTop: number | null): void {
  if (scrollTop === null) {
    const existing = drafts.get(agentId)
    if (!existing) return
    existing.scrollTop = null
    pruneIfEmpty(agentId, existing)
    return
  }
  const entry = ensureEntry(agentId)
  entry.scrollTop = scrollTop
}

export function clearChatDraft(agentId: string): void {
  const existing = drafts.get(agentId)
  if (existing) {
    existing.text = ''
    existing.images = []
    pruneIfEmpty(agentId, existing)
  }
  writeStoredText(agentId, '')
}

// Reset module and sessionStorage draft state between unit tests.
export function resetChatDraftsForTest(): void {
  drafts.clear()
  persistedTextAgents.clear()
  try {
    window.sessionStorage.removeItem(CHAT_DRAFTS_STORAGE_KEY)
  } catch {
    // Ignore storage errors in restricted environments.
  }
}
