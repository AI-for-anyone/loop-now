// Navigation carrying this state skips the “有未保存的修改” (You have unsaved changes) prompt, e.g. returning to the list after an
// Agent is deleted.
export const skipUnsavedPrompt = { skipUnsavedPrompt: true } as const

export function skipsUnsavedPrompt(state: unknown): boolean {
  return typeof state === 'object' && state !== null && (state as { skipUnsavedPrompt?: unknown }).skipUnsavedPrompt === true
}
