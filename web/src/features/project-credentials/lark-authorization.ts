import type { Schemas } from '@/api/client'

// Same fixed issuer/path/query contract as larkauth.ValidAuthorizationURL.
// User-visible user_code is allowed; device_code and arbitrary redirect/token
// parameters are not. No navigation, persistence or OAuth side effects here.
export function larkAuthorizationUrl(value: string, brand: Schemas['LarkApplication']['brand']): boolean {
  try {
    const host = brand === 'feishu' ? 'accounts.feishu.cn' : 'accounts.larksuite.com'
    if (new TextEncoder().encode(value).length > 4096 || !value.startsWith(`https://${host}/`) || /%(?![0-9a-f]{2})/i.test(value) || hasControl(value) || value.includes(' ') || value.includes('#')) return false
    const url = new URL(value)
    if (url.origin !== `https://${host}` || url.pathname !== '/oauth/v1/device/verify' || url.username || url.password || url.search.includes(';')) return false
    for (const [key, text] of url.searchParams) {
      if (!['flow_id', 'user_code'].includes(key) || url.searchParams.getAll(key).length !== 1 || !text || text !== text.trim() || hasControl(text) || new TextEncoder().encode(text).length > 200) return false
    }
    return true
  } catch { return false }
}
function hasControl(value: string) {
  return [...value].some(character => { const point = character.codePointAt(0) ?? 0; return point < 32 || point >= 127 && point <= 159 })
}
