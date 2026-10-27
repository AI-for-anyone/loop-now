// 带上这个导航状态的跳转不经过“有未保存的修改”提示，例如删除 Agent 成功后回到列表。
export const skipUnsavedPrompt = { skipUnsavedPrompt: true } as const

export function skipsUnsavedPrompt(state: unknown): boolean {
  return typeof state === 'object' && state !== null && (state as { skipUnsavedPrompt?: unknown }).skipUnsavedPrompt === true
}
