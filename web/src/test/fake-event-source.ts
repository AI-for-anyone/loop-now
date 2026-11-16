// EventSource for tests: test code pushes events and opens or closes the connection itself.
type Listener = (event: MessageEvent<string>) => void

export class FakeEventSource {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2
  static instances: FakeEventSource[] = []

  readonly CONNECTING = 0
  readonly OPEN = 1
  readonly CLOSED = 2
  readonly url: string
  readonly withCredentials = false
  readyState = FakeEventSource.CONNECTING
  onopen: ((event: Event) => void) | null = null
  onerror: ((event: Event) => void) | null = null
  onmessage: Listener | null = null
  private listeners = new Map<string, Set<Listener>>()

  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }

  static latest(): FakeEventSource {
    const source = FakeEventSource.instances.at(-1)
    if (!source) throw new Error('没有创建过 EventSource')
    return source
  }

  static reset() {
    FakeEventSource.instances = []
  }

  addEventListener(type: string, listener: Listener) {
    if (!this.listeners.has(type)) this.listeners.set(type, new Set())
    this.listeners.get(type)!.add(listener)
  }

  removeEventListener(type: string, listener: Listener) {
    this.listeners.get(type)?.delete(listener)
  }

  close() {
    this.readyState = FakeEventSource.CLOSED
  }

  // ---- Test controls ----

  open() {
    this.readyState = FakeEventSource.OPEN
    this.onopen?.(new Event('open'))
  }

  emit(type: string, data: unknown, id = '') {
    const event = new MessageEvent<string>(type, { data: JSON.stringify(data), lastEventId: id })
    for (const listener of this.listeners.get(type) ?? []) listener(event)
  }

  // Simulates the browser's automatic reconnect: the connection dropped but is still reconnecting.
  interrupt() {
    this.readyState = FakeEventSource.CONNECTING
    this.onerror?.(new Event('error'))
  }

  // Simulates the browser closing the connection for good (e.g. the server returned an error status).
  fail() {
    this.readyState = FakeEventSource.CLOSED
    this.onerror?.(new Event('error'))
  }
}
