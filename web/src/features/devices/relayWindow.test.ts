import { afterEach, describe, expect, it, vi } from 'vitest'
import { isAllowedRelaySignInUrl, reserveRelaySignInWindow } from './relayWindow'

afterEach(() => vi.restoreAllMocks())

describe('isAllowedRelaySignInUrl', () => {
  it('allows https relay URLs and loopback http URLs only', () => {
    expect(isAllowedRelaySignInUrl('https://relay.loop-now.app/connect?state=abc')).toBe(true)
    expect(isAllowedRelaySignInUrl('http://127.0.0.1:8787/connect?state=abc')).toBe(true)
    expect(isAllowedRelaySignInUrl('http://localhost:8787/connect?state=abc')).toBe(true)
    expect(isAllowedRelaySignInUrl('http://[::1]:8787/connect?state=abc')).toBe(true)
    expect(isAllowedRelaySignInUrl('http://relay.example.test/connect')).toBe(false)
    expect(isAllowedRelaySignInUrl('javascript:alert(1)')).toBe(false)
    expect(isAllowedRelaySignInUrl('not a url')).toBe(false)
  })
})

describe('reserveRelaySignInWindow', () => {
  function popup() {
    const child = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    return { child, open }
  }

  it('reserves a blank window synchronously, clears opener, navigates to https or loopback http, and leaves navigated windows open', () => {
    const { child, open } = popup()
    const target = reserveRelaySignInWindow()
    expect(open).toHaveBeenCalledWith('about:blank', '_blank')
    expect(child.opener).toBeNull()
    expect(target.navigate('https://relay.loop-now.app/connect?state=1')).toBe(true)
    expect(child.location.replace).toHaveBeenCalledWith('https://relay.loop-now.app/connect?state=1')
    target.closeUnused()
    expect(child.close).not.toHaveBeenCalled()

    const local = popup()
    const localTarget = reserveRelaySignInWindow()
    expect(localTarget.navigate('http://127.0.0.1:8787/connect?state=2')).toBe(true)
    expect(local.child.location.replace).toHaveBeenCalledWith('http://127.0.0.1:8787/connect?state=2')
  })

  it('returns false when popups are blocked and closes the blank window on disallowed URLs', () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    expect(reserveRelaySignInWindow().navigate('https://relay.loop-now.app/connect')).toBe(false)

    const { child } = popup()
    const target = reserveRelaySignInWindow()
    expect(target.navigate('http://evil.example.test/connect')).toBe(false)
    expect(child.location.replace).not.toHaveBeenCalled()
    expect(child.close).toHaveBeenCalledOnce()
  })
})
