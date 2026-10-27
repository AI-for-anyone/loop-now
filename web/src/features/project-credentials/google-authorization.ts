// Fixed Google SDK568 issuer/query contract, independently reviewed with server.
const required = ['client_id', 'response_type', 'redirect_uri', 'scope', 'state', 'code_challenge', 'code_challenge_method', 'access_type']
const allowed = new Set([...required, 'prompt', 'include_granted_scopes', 'login_hint'])
const prompts = new Set(['consent', 'select_account', 'consent select_account', 'select_account consent'])
export function googleAuthorizationUrl(value: string): boolean {
  try {
    if (value.length > 16384 || /%(?![0-9a-f]{2})/i.test(value)) return false
    const url = new URL(value), query = url.searchParams
    if (url.origin !== 'https://accounts.google.com' || !['/o/oauth2/auth', '/o/oauth2/v2/auth'].includes(url.pathname) || url.username || url.password || url.hash) return false
    if (required.some(key => !query.get(key)?.trim())) return false
    if (query.get('response_type') !== 'code' || query.get('code_challenge_method') !== 'S256' || query.get('access_type') !== 'offline') return false
    if (!['http://localhost:8085/', 'http://127.0.0.1:8085/'].includes(query.get('redirect_uri')!)) return false
    if (query.has('prompt') && !prompts.has(query.get('prompt')!)) return false
    if (query.has('include_granted_scopes') && !['true', 'false'].includes(query.get('include_granted_scopes')!)) return false
    if (query.has('login_hint') && !query.get('login_hint')?.trim()) return false
    if (url.search.includes(';')) return false
    return [...query.keys()].every(key => allowed.has(key) && query.getAll(key).length === 1 && !!query.get(key) && !/[\r\n\0]/.test(query.get(key)!))
  } catch { return false }
}
