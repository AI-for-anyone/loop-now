// Matches the pinned Pi 1.1.0 and the backend's safe URL contract: the organization switch with "token" in its name is not a token payload.
// The allowed parameters are listed exactly; unknown keys (including the secret keys code/token/verifier) are still rejected.
const authorizationParameters = new Set(['response_type', 'client_id', 'redirect_uri', 'scope', 'code_challenge', 'code_challenge_method', 'state', 'id_token_add_organizations', 'codex_cli_simplified_flow', 'originator'])
// The host name of the authorization URL or of the code-entry URL, used in link text; returned unchanged if it can't be parsed.
export const hostOf = (url: string) => { try { return new URL(url).host } catch { return url } }

// A plugin provider's URL comes from the extension and has already been validated by the server; only the generic check is done here:
// https, no username or password, no fragment.
export function pluginAuthorizationUrl(value: string): boolean {
  try {
    const url = new URL(value)
    return url.protocol === 'https:' && !url.username && !url.password && !url.hash && !value.includes('#')
  } catch { return false }
}

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
