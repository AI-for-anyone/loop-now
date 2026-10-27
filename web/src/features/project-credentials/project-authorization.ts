// 与固定Pi1.1.0及后端safe URL契约一致：token字样的组织开关不是令牌载荷。
// 精确列出允许的参数，未知键（含code/token/verifier秘密键）仍拒绝。
const authorizationParameters = new Set(['response_type', 'client_id', 'redirect_uri', 'scope', 'code_challenge', 'code_challenge_method', 'state', 'id_token_add_organizations', 'codex_cli_simplified_flow', 'originator'])
export function projectAuthorizationUrl(value: string): boolean {
  try {
    const url = new URL(value)
    if (url.origin !== 'https://auth.openai.com' || url.pathname !== '/oauth/authorize' || url.username || url.password || url.hash) return false
    const query = url.searchParams
    if (!query.get('state') || !query.get('code_challenge')) return false
    if (query.has('redirect_uri') && query.get('redirect_uri') !== 'http://localhost:1455/auth/callback') return false
    return [...query.keys()].every(key => authorizationParameters.has(key) && query.getAll(key).length === 1)
  } catch { return false }
}
