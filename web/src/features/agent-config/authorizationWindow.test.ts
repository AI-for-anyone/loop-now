import { afterEach, describe, expect, it, vi } from 'vitest'
import { reserveAuthorizationWindow } from './authorizationWindow'

afterEach(() => vi.restoreAllMocks())

describe('Provider authorization window', () => {
  function popup() {
    const child = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    return { child, open }
  }

  it('reserves the window when called on click, cuts the opener, then navigates; closing the page does not close a navigated window', () => {
    const { child, open } = popup()
    const target = reserveAuthorizationWindow()
    expect(open).toHaveBeenCalledWith('about:blank', '_blank')
    expect(child.opener).toBeNull()
    expect(target.navigate('https://auth.example.test/authorize')).toBe(true)
    expect(child.location.replace).toHaveBeenCalledWith('https://auth.example.test/authorize')
    target.closeUnused()
    expect(child.close).not.toHaveBeenCalled()
  })

  it('returns failure when the popup is blocked or already closed by the user, so the page can show a manual open link', () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    expect(reserveAuthorizationWindow().navigate('https://auth.example.test/authorize')).toBe(false)
    const { child } = popup()
    const target = reserveAuthorizationWindow()
    child.closed = true
    expect(target.navigate('https://auth.example.test/authorize')).toBe(false)
  })

  it('rejects non-HTTPS URLs and closes the unused blank window after the failure', () => {
    const { child } = popup()
    const target = reserveAuthorizationWindow()
    expect(target.navigate('javascript:alert(1)')).toBe(false)
    expect(target.navigate('http://auth.example.test')).toBe(false)
    expect(child.location.replace).not.toHaveBeenCalled()
    target.closeUnused()
    expect(child.close).toHaveBeenCalledOnce()
  })
  it('a navigation that throws also closes the blank window once; a window that already reached the provider is not closed on later errors', () => {
    const { child } = popup()
    child.location.replace.mockImplementationOnce(() => { throw new Error('navigation rejected') })
    const failed = reserveAuthorizationWindow()
    expect(failed.navigate('https://auth.example.test/authorize')).toBe(false)
    failed.closeUnused()
    expect(child.close).toHaveBeenCalledOnce()
    const other = popup()
    const reached = reserveAuthorizationWindow()
    expect(reached.navigate('https://auth.example.test/authorize')).toBe(true)
    other.child.location.replace.mockImplementationOnce(() => { throw new Error('later navigation rejected') })
    expect(reached.navigate('https://auth.example.test/authorize')).toBe(false)
    reached.closeUnused()
    expect(other.child.close).not.toHaveBeenCalled()
  })

})
