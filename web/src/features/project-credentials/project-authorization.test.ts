import { describe, expect, it } from 'vitest'
import { projectAuthorizationUrl } from './project-authorization'

const base = 'https://auth.openai.com/oauth/authorize?client_id=fixture&response_type=code&state=fixture&code_challenge=fixture&code_challenge_method=S256'
describe('Project short-lived authorization entry', () => {
  it('allows the formal Pi organization switches and does not mistake token in a parameter key for a token payload', () => {
    expect(projectAuthorizationUrl(`${base}&id_token_add_organizations=true&codex_cli_simplified_flow=true&originator=pi&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback`)).toBe(true)
  })
  it.each(['code', 'access_token', 'refresh_token', 'id_token', 'code_verifier', 'unknown_parameter'])('still rejects the secret or unknown parameter key %s', key => {
    expect(projectAuthorizationUrl(`${base}&${key}=fixture`)).toBe(false)
  })
  it.each([
    base.replace('https:', 'http:'), base.replace('auth.openai.com', 'other.test'), base.replace('/oauth/authorize', '/other'),
    base.replace('https://', 'https://user@'), `${base}#fragment`, `${base}&state=duplicate`, `${base}&id_token_add_organizations=true&id_token_add_organizations=true`,
    `${base}&redirect_uri=https%3A%2F%2Fother.test%2Fcallback`, base.replace('state=fixture', 'state='), base.replace('&code_challenge=fixture', ''),
  ])('rejects samples violating the fixed scope, PKCE or duplicate-parameter rules %#', value => expect(projectAuthorizationUrl(value)).toBe(false))
})
