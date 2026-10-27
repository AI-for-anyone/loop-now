import { afterEach, describe, expect, it, vi } from 'vitest'
import { reserveAuthorizationWindow } from './authorizationWindow'

afterEach(() => vi.restoreAllMocks())

describe('供应商授权窗口', () => {
  function popup() {
    const child = { opener: window, closed: false, location: { replace: vi.fn() }, close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue(child as unknown as Window)
    return { child, open }
  }

  it('在点击调用时预留窗口、断开 opener，再导航；关闭页面不关闭已导航窗口', () => {
    const { child, open } = popup()
    const target = reserveAuthorizationWindow()
    expect(open).toHaveBeenCalledWith('about:blank', '_blank')
    expect(child.opener).toBeNull()
    expect(target.navigate('https://auth.example.test/authorize')).toBe(true)
    expect(child.location.replace).toHaveBeenCalledWith('https://auth.example.test/authorize')
    target.closeUnused()
    expect(child.close).not.toHaveBeenCalled()
  })

  it('弹窗拦截或已被用户关闭时，返回失败供页面显示手动打开入口', () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    expect(reserveAuthorizationWindow().navigate('https://auth.example.test/authorize')).toBe(false)
    const { child } = popup()
    const target = reserveAuthorizationWindow()
    child.closed = true
    expect(target.navigate('https://auth.example.test/authorize')).toBe(false)
  })

  it('拒绝非 HTTPS 地址，失败后关闭未使用的空窗口', () => {
    const { child } = popup()
    const target = reserveAuthorizationWindow()
    expect(target.navigate('javascript:alert(1)')).toBe(false)
    expect(target.navigate('http://auth.example.test')).toBe(false)
    expect(child.location.replace).not.toHaveBeenCalled()
    target.closeUnused()
    expect(child.close).toHaveBeenCalledOnce()
  })
  it('导航抛错也关闭一次空白窗；已到供应商的窗口遇后续异常不关闭', () => {
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
