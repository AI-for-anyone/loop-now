// 展示层编辑状态，不是HTTP schema。API原文/版本由调用方映射为这对值。
export type PromptText = Readonly<{ text: string; version: number }>
export interface PromptDraft {
  base: PromptText
  latest: PromptText
  draft: string
  pinned: boolean
  pending: boolean
  submitted: PromptText | null
  conflict: boolean
  review: PromptText | null
  reading: boolean
  readFailed: boolean
}
export type PromptEvent =
  | { type: 'latest'; value: PromptText }
  | { type: 'edit'; text: string }
  | { type: 'saving' }
  | { type: 'saved'; value: PromptText }
  | { type: 'failed' }
  | { type: 'conflict' }
  | { type: 'reading' }
  | { type: 'reviewed'; value: PromptText }
  | { type: 'readFailed' }
  | { type: 'adopt' }
const copy = (value: PromptText): PromptText => ({ text: value.text, version: value.version })
export function createPromptDraft(value: PromptText): PromptDraft {
  return { base: copy(value), latest: copy(value), draft: value.text, pinned: false, pending: false, submitted: null, conflict: false, review: null, reading: false, readFailed: false }
}
export function promptDirty(state: PromptDraft) {
  return state.draft !== state.base.text || (state.pinned && state.latest.version !== state.base.version)
}
export function reducePromptDraft(state: PromptDraft, event: PromptEvent): PromptDraft {
  switch (event.type) {
    case 'latest': {
      if (event.value.version < state.latest.version) return state
      const latest = copy(event.value)
      if (state.pinned || state.pending || state.conflict) return { ...state, latest }
      return { ...state, base: latest, latest, draft: latest.text }
    }
    case 'edit': return { ...state, draft: event.text, pinned: true }
    case 'saving': return { ...state, pending: true, submitted: { text: state.draft, version: state.base.version } }
    case 'saved': {
      const saved = copy(event.value)
      const latest = state.latest.version > saved.version ? state.latest : saved
      const changed = state.submitted !== null && state.draft !== state.submitted.text
      return { ...state, base: changed ? saved : latest, latest, draft: changed ? state.draft : latest.text, pinned: changed, pending: false, submitted: null, conflict: false, review: null, reading: false, readFailed: false }
    }
    case 'failed': return { ...state, pending: false, submitted: null }
    case 'conflict': return { ...state, pending: false, submitted: null, conflict: true, review: null, reading: true, readFailed: false }
    case 'reading': return { ...state, reading: true, readFailed: false }
    case 'reviewed': return { ...state, latest: event.value.version >= state.latest.version ? copy(event.value) : state.latest, review: copy(event.value), reading: false, readFailed: false }
    case 'readFailed': return { ...state, review: null, reading: false, readFailed: true }
    case 'adopt': return state.review ? { ...state, base: copy(state.review), pinned: true, conflict: false, review: null } : state
  }
}
