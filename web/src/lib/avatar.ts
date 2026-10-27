// 头像的固定色对：蓝/粉、绿/蓝、粉/黄、黄/蓝。
export const AVATAR_PAIRS = [
  ['var(--riso-blue)', 'var(--riso-pink)'],
  ['var(--green)', 'var(--riso-blue)'],
  ['var(--riso-pink)', 'var(--riso-yellow)'],
  ['var(--riso-yellow)', 'var(--riso-blue)'],
] as const

// 对 Agent ID 做 FNV-1a 哈希，按余数从固定色对中选一组；同一个 ID 总是得到同一组颜色。
export function avatarPair(id: string): (typeof AVATAR_PAIRS)[number] {
  let hash = 0x811c9dc5
  for (let i = 0; i < id.length; i++) {
    hash ^= id.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193) >>> 0
  }
  return AVATAR_PAIRS[hash % AVATAR_PAIRS.length]
}
