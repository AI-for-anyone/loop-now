import { afterEach, describe, expect, it, vi } from 'vitest'
import { copyText } from './clipboard'

const clipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard')
const setClipboard = (value: unknown) => Object.defineProperty(navigator, 'clipboard', { configurable: true, value })
const setExec = (impl: ((command: string) => boolean) | undefined) => Object.defineProperty(document, 'execCommand', { configurable: true, value: impl })

afterEach(() => {
  if (clipboard) Object.defineProperty(navigator, 'clipboard', clipboard)
  else Reflect.deleteProperty(navigator, 'clipboard')
  Reflect.deleteProperty(document, 'execCommand')
})

describe('写剪贴板', () => {
  it('浏览器允许时直接写入', async () => {
    const writeText = vi.fn(() => Promise.resolve())
    setClipboard({ writeText })
    await expect(copyText('内容')).resolves.toBe(true)
    expect(writeText).toHaveBeenCalledWith('内容')
  })

  it('被拒绝时（不在按键手势内）改用旧的复制命令；它成功就算成功，用过的临时输入框要拿掉', async () => {
    setClipboard({ writeText: () => Promise.reject(new DOMException('denied', 'NotAllowedError')) })
    let copied = ''
    setExec((command) => {
      copied = command === 'copy' ? (document.activeElement as HTMLTextAreaElement).value : ''
      return true
    })
    await expect(copyText('第二种办法')).resolves.toBe(true)
    expect(copied).toBe('第二种办法')
    expect(document.querySelector('textarea')).toBeNull()
  })

  it('用普通 http 的局域网地址打开时没有 navigator.clipboard：不抛错，改用旧的复制命令', async () => {
    setClipboard(undefined)
    setExec(() => true)
    await expect(copyText('内容')).resolves.toBe(true)
  })

  it('两种办法都不行时返回 false，不抛错', async () => {
    setClipboard(undefined)
    setExec(() => false)
    await expect(copyText('内容')).resolves.toBe(false)
    setExec(() => { throw new Error('unsupported') })
    await expect(copyText('内容')).resolves.toBe(false)
    setExec(undefined)
    await expect(copyText('内容')).resolves.toBe(false)
  })
})
