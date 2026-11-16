// The fixed color pairs for avatars: blue/pink, green/blue, pink/yellow, yellow/blue.
export const AVATAR_PAIRS = [
  ['var(--riso-blue)', 'var(--riso-pink)'],
  ['var(--green)', 'var(--riso-blue)'],
  ['var(--riso-pink)', 'var(--riso-yellow)'],
  ['var(--riso-yellow)', 'var(--riso-blue)'],
] as const

// FNV-1a hash of the Agent ID, with the remainder picking one of the fixed color pairs; the same ID always gets the same pair.
export function avatarPair(id: string): (typeof AVATAR_PAIRS)[number] {
  let hash = 0x811c9dc5
  for (let i = 0; i < id.length; i++) {
    hash ^= id.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193) >>> 0
  }
  return AVATAR_PAIRS[hash % AVATAR_PAIRS.length]
}
