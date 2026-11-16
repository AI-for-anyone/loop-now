import { afterEach, expect, it, vi } from 'vitest'
import { http, HttpResponse } from 'msw'
import { apiUrl } from '@/test/api'
import { server } from '@/test/server'
import { downloadAgentSession } from './session-export'

const originals = (['createObjectURL', 'revokeObjectURL'] as const).map((key) => [key, Object.getOwnPropertyDescriptor(URL, key)] as const)

afterEach(() => {
  vi.restoreAllMocks()
  for (const [key, descriptor] of originals) {
    if (descriptor) Object.defineProperty(URL, key, descriptor)
    else Reflect.deleteProperty(URL, key)
  }
})

// The browser starts reading the blob asynchronously, only after the click: if the URL is revoked right away,
// Firefox and Safari may not be able to read the content, and the download fails while the UI still reports success.
it('触发下载之后不立刻撤销 blob 地址，过一会儿再撤销', async () => {
  const revoke = vi.fn()
  Object.defineProperty(URL, 'createObjectURL', { configurable: true, value: () => 'blob:session' })
  Object.defineProperty(URL, 'revokeObjectURL', { configurable: true, value: revoke })
  const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
  const timers = vi.spyOn(window, 'setTimeout')
  server.use(http.get(apiUrl('/agents/a1/session/export'), () => new HttpResponse('{}', { headers: { 'Content-Disposition': 'attachment; filename="session.jsonl"' } })))

  await downloadAgentSession('a1', 'jsonl')

  expect(click).toHaveBeenCalledTimes(1)
  expect(revoke).not.toHaveBeenCalled()
  expect(document.querySelector('a[download]')).toBeNull()
  const later = timers.mock.calls.find(([, delay]) => typeof delay === 'number' && delay >= 30_000)
  expect(later).toBeDefined()
  ;(later![0] as () => void)()
  expect(revoke).toHaveBeenCalledWith('blob:session')
})
