import { describe, expect, it } from 'vitest'
import { googleAuthorizationUrl } from './google-authorization'

export const googleURL = 'https://accounts.google.com/o/oauth2/auth?client_id=fixture-client&response_type=code&redirect_uri=http%3A%2F%2Flocalhost%3A8085%2F&scope=fixture-scope&state=fixture-state&code_challenge=fixture-challenge&code_challenge_method=S256&access_type=offline'
describe('固定Google SDK授权URL合同', () => {
  it('仅接受正式两个路径、8085回调与精确可选值', () => {
    expect(googleAuthorizationUrl(googleURL)).toBe(true)
    expect(googleAuthorizationUrl(googleURL.replace('/o/oauth2/auth', '/o/oauth2/v2/auth') + '&prompt=select_account%20consent&include_granted_scopes=false&login_hint=fixture')).toBe(true)
    expect(googleAuthorizationUrl(googleURL.replace('localhost%3A8085', '127.0.0.1%3A8085'))).toBe(true)
  })
  it.each(['code', 'access_token', 'refresh_token', 'id_token', 'code_verifier', 'token', 'unknown', 'id_token_add_organizations'])('拒绝秘密或未定参数%s', key => {
    expect(googleAuthorizationUrl(`${googleURL}&${key}=fixture`)).toBe(false)
  })
  it.each([
    googleURL.replace('https:', 'http:'), googleURL.replace('accounts.google.com', 'accounts.google.com.example.test'),
    googleURL.replace('accounts.google.com', 'user@accounts.google.com'), googleURL.replace('accounts.google.com', 'accounts.google.com:444'),
    googleURL + '#fragment', googleURL + '&state=duplicate', googleURL.replace('S256', 'plain'),
    googleURL.replace('localhost%3A8085', 'localhost%3A1455'), googleURL.replace('access_type=offline', 'access_type=online'),
    googleURL.replace('&state=fixture-state', ''), googleURL + '&prompt=none', googleURL + '&include_granted_scopes=1',
    googleURL.replace('fixture-state', 'bad%0Astate'), googleURL.replace('fixture-client', 'bad%00client'), googleURL + '&login_hint=%',
  ])('拒绝越界issuer/callback/重复或不安全值', value => expect(googleAuthorizationUrl(value)).toBe(false))
})
