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

describe('writing to the clipboard', () => {
  it('writes directly when the browser allows it', async () => {
    const writeText = vi.fn(() => Promise.resolve())
    setClipboard({ writeText })
    await expect(copyText('content')).resolves.toBe(true)
    expect(writeText).toHaveBeenCalledWith('content')
  })

  it('when refused (outside a key gesture), falls back to the old copy command; its success counts as success, and the temporary input it used is removed', async () => {
    setClipboard({ writeText: () => Promise.reject(new DOMException('denied', 'NotAllowedError')) })
    let copied = ''
    setExec((command) => {
      copied = command === 'copy' ? (document.activeElement as HTMLTextAreaElement).value : ''
      return true
    })
    await expect(copyText('second way')).resolves.toBe(true)
    expect(copied).toBe('second way')
    expect(document.querySelector('textarea')).toBeNull()
  })

  it('opened on a plain-http LAN address there is no navigator.clipboard: does not throw, falls back to the old copy command', async () => {
    setClipboard(undefined)
    setExec(() => true)
    await expect(copyText('content')).resolves.toBe(true)
  })

  it('returns false without throwing when neither way works', async () => {
    setClipboard(undefined)
    setExec(() => false)
    await expect(copyText('content')).resolves.toBe(false)
    setExec(() => { throw new Error('unsupported') })
    await expect(copyText('content')).resolves.toBe(false)
    setExec(undefined)
    await expect(copyText('content')).resolves.toBe(false)
  })
})
