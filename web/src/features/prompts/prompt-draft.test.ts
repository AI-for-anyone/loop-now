import { describe, expect, it } from 'vitest'
import { createPromptDraft, promptDirty, reducePromptDraft as update } from './prompt-draft'

describe('Prompt编辑基线与固定核对版本', () => {
  it('未编辑时跟随服务器；编辑后保留原版本，即使最新文本恰好等于草稿', () => {
    let state = update(createPromptDraft({ text: 'A', version: 1 }), { type: 'latest', value: { text: 'B', version: 2 } })
    expect(state.base.version).toBe(2)
    state = update(state, { type: 'edit', text: 'C' })
    state = update(state, { type: 'latest', value: { text: 'C', version: 3 } })
    expect(state.base).toEqual({ text: 'B', version: 2 })
    expect(state.draft).toBe('C'); expect(promptDirty(state)).toBe(true)
    state = update(state, { type: 'saving' })
    expect(state.submitted).toEqual({ text: 'C', version: 2 })
  })
  it('核对V2后收到V3，只采纳已核对V2，草稿保持，下一次仍提交V2', () => {
    let state = update(createPromptDraft({ text: 'A', version: 1 }), { type: 'edit', text: 'D' })
    state = update(state, { type: 'conflict' })
    const read = { text: 'B', version: 2 }
    state = update(state, { type: 'reviewed', value: read })
    read.text = '不能改变已核对内容'
    state = update(state, { type: 'latest', value: { text: 'C', version: 3 } })
    expect(state.review).toEqual({ text: 'B', version: 2 })
    state = update(state, { type: 'adopt' })
    expect(state.base).toEqual({ text: 'B', version: 2 }); expect(state.draft).toBe('D')
    state = update(state, { type: 'saving' }); expect(state.submitted?.version).toBe(2)
  })
  it('草稿等于已核对文本时，V3也不能在采纳V2后悄改基线', () => {
    let state = update(createPromptDraft({ text: 'A', version: 1 }), { type: 'edit', text: 'B' })
    state = update(state, { type: 'conflict' })
    state = update(state, { type: 'reviewed', value: { text: 'B', version: 2 } })
    state = update(state, { type: 'latest', value: { text: 'C', version: 3 } })
    state = update(state, { type: 'adopt' })
    state = update(state, { type: 'latest', value: { text: 'C', version: 3 } })
    expect(state.base.version).toBe(2); expect(state.draft).toBe('B'); expect(promptDirty(state)).toBe(true)
  })
  it('412后读取失败保留草稿/基线，没有可采纳的未知版本', () => {
    let state = update(createPromptDraft({ text: 'A', version: 1 }), { type: 'edit', text: '' })
    state = update(state, { type: 'conflict' }); state = update(state, { type: 'readFailed' })
    expect(state.draft).toBe(''); expect(state.base.version).toBe(1); expect(state.review).toBeNull()
    expect(update(state, { type: 'adopt' })).toBe(state)
  })
  it('保存成功采用返回版本，不接受落后的后台响应；保存期间后续编辑不丢', () => {
    let state = update(createPromptDraft({ text: 'A', version: 1 }), { type: 'edit', text: 'B' })
    state = update(state, { type: 'saving' }); state = update(state, { type: 'edit', text: 'C' })
    state = update(state, { type: 'saved', value: { text: 'B', version: 2 } })
    expect(state.draft).toBe('C'); expect(state.base.version).toBe(2); expect(promptDirty(state)).toBe(true)
    state = update(state, { type: 'latest', value: { text: 'A', version: 1 } })
    expect(state.base.version).toBe(2)
  })
})
