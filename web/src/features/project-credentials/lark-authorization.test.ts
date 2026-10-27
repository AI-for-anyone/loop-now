import { describe, expect, it } from 'vitest'
import { larkAuthorizationUrl } from './lark-authorization'

const base = 'https://accounts.feishu.cn/oauth/v1/device/verify'
describe('Lark fixed official public authorization URI', () => {
  it('accepts only the captured brand, optional official visible code and flow identifier', () => {
    expect(larkAuthorizationUrl(base, 'feishu')).toBe(true)
    expect(larkAuthorizationUrl(`${base}?flow_id=synthetic&user_code=visible`, 'feishu')).toBe(true)
    expect(larkAuthorizationUrl(base.replace('feishu.cn', 'larksuite.com'), 'lark')).toBe(true)
    expect(larkAuthorizationUrl(base, 'lark')).toBe(false)
  })
  it.each(['device_code', 'access_token', 'refresh_token', 'client_secret', 'redirect_uri', 'state', 'code', 'unknown'])('rejects secret or unrelated parameter %s', name => {
    expect(larkAuthorizationUrl(`${base}?${name}=synthetic`, 'feishu')).toBe(false)
  })
  it.each([
    base.replace('https:', 'http:'), base.replace('accounts.feishu.cn', 'accounts.feishu.cn.evil.test'),
    base.replace('https://', 'https://user@'), base.replace('.cn/', '.cn:443/'),
    `${base}#fragment`, `${base}?flow_id=a&flow_id=b`, `${base}?flow_id=%0a`, `${base}?flow_id=%ZZ`,
    `${base}?flow_id=`, `${base}?user_code=${'界'.repeat(67)}`, `${base}?flow_id=a;user_code=b`, `${base}/extra`,
  ])('rejects invalid issuer/query/path boundary %s', value => {
    expect(larkAuthorizationUrl(value, 'feishu')).toBe(false)
  })
})
