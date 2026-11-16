import { describe, expect, it } from 'vitest'
import { createPromptDraft, promptDirty, reducePromptDraft as update } from './prompt-draft'

describe('Prompt editing base and the pinned reviewed version', () => {
  it('follows the server while unedited; after an edit keeps the original version, even when the latest text happens to equal the draft', () => {
    let state = update(createPromptDraft({ text: 'A', version: 1 }), { type: 'latest', value: { text: 'B', version: 2 } })
    expect(state.base.version).toBe(2)
    state = update(state, { type: 'edit', text: 'C' })
    state = update(state, { type: 'latest', value: { text: 'C', version: 3 } })
    expect(state.base).toEqual({ text: 'B', version: 2 })
    expect(state.draft).toBe('C'); expect(promptDirty(state)).toBe(true)
    state = update(state, { type: 'saving' })
    expect(state.submitted).toEqual({ text: 'C', version: 2 })
  })
  it('receiving V3 after reviewing V2 adopts only the reviewed V2, keeps the draft, and the next save still submits V2', () => {
    let state = update(createPromptDraft({ text: 'A', version: 1 }), { type: 'edit', text: 'D' })
    state = update(state, { type: 'conflict' })
    const read = { text: 'B', version: 2 }
    state = update(state, { type: 'reviewed', value: read })
    read.text = 'must not change the reviewed content'
    state = update(state, { type: 'latest', value: { text: 'C', version: 3 } })
    expect(state.review).toEqual({ text: 'B', version: 2 })
    state = update(state, { type: 'adopt' })
    expect(state.base).toEqual({ text: 'B', version: 2 }); expect(state.draft).toBe('D')
    state = update(state, { type: 'saving' }); expect(state.submitted?.version).toBe(2)
  })
  it('when the draft equals the reviewed text, V3 still cannot silently change the base after V2 is adopted', () => {
    let state = update(createPromptDraft({ text: 'A', version: 1 }), { type: 'edit', text: 'B' })
    state = update(state, { type: 'conflict' })
    state = update(state, { type: 'reviewed', value: { text: 'B', version: 2 } })
    state = update(state, { type: 'latest', value: { text: 'C', version: 3 } })
    state = update(state, { type: 'adopt' })
    state = update(state, { type: 'latest', value: { text: 'C', version: 3 } })
    expect(state.base.version).toBe(2); expect(state.draft).toBe('B'); expect(promptDirty(state)).toBe(true)
  })
  it('a failed read after a 412 keeps the draft/base, with no unknown version to adopt', () => {
    let state = update(createPromptDraft({ text: 'A', version: 1 }), { type: 'edit', text: '' })
    state = update(state, { type: 'conflict' }); state = update(state, { type: 'readFailed' })
    expect(state.draft).toBe(''); expect(state.base.version).toBe(1); expect(state.review).toBeNull()
    expect(update(state, { type: 'adopt' })).toBe(state)
  })
  it('a successful save adopts the returned version and rejects stale background responses; edits made while saving are not lost', () => {
    let state = update(createPromptDraft({ text: 'A', version: 1 }), { type: 'edit', text: 'B' })
    state = update(state, { type: 'saving' }); state = update(state, { type: 'edit', text: 'C' })
    state = update(state, { type: 'saved', value: { text: 'B', version: 2 } })
    expect(state.draft).toBe('C'); expect(state.base.version).toBe(2); expect(promptDirty(state)).toBe(true)
    state = update(state, { type: 'latest', value: { text: 'A', version: 1 } })
    expect(state.base.version).toBe(2)
  })
})
